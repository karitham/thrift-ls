package lsp

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/formatter"
	"github.com/karitham/thrift-ls/lsp/source"
	"github.com/karitham/thrift-ls/options"
	"github.com/karitham/thrift-ls/sema"
	"github.com/karitham/thrift-ls/store"
	"github.com/karitham/thrift-ls/vfs"
)

type Server struct {
	session *store.Session

	client protocol.Client

	// defaults sits directly above the builtin defaults.
	defaults options.Patch
	// configSource resolves the config document per project root.
	// Nil means file discovery through the server's Files.
	configSource ConfigSource
	version      string

	// cli overlays every view's config.
	cli options.Patch

	// workspaceOverlay is the last accepted workspace settings, overlaid
	// on every view's config. Guarded by optsMu.
	optsMu           sync.RWMutex
	workspaceOverlay options.Patch

	// logLevel is the first view config's log level, applied once the
	// workspace is known. Guarded by logLevelMu.
	logLevelMu sync.Mutex
	logLevel   *int

	workspace *workspace
	analysis  Analysis

	// configs holds each view folder's resolved configuration. Views only
	// carry what the store needs (include paths); formatting settings and
	// log level stay here, where the workspace overlay applies.
	cfgMu   sync.RWMutex
	configs map[uri.URI]options.Patch

	// configIssues remembers why a folder's own thrift-ls.json could not
	// be used, so the reason can be surfaced to the user (who only sees
	// formatter defaults silently otherwise).
	configIssues map[uri.URI]configIssue

	// workspaceWalkOnce guards the workspace load on Initialized so the
	// initialize handshake never blocks on parsing the workspace.
	workspaceWalkOnce sync.Once

	// analysisMu serializes analyzer instances shared by diagnostic workers.
	analysisMu sync.Mutex

	// diagSync runs diagnostics inline instead of in a background goroutine.
	// It is a test hook: in-package tests drive requests synchronously and
	// assert on reports without channels or virtual time.
	diagSync bool

	// lastReport remembers the diagnostics the server last published per
	// file, so code actions can pair fixes with the diagnostics without a
	// round trip through the client. Guarded by reportMu.
	reportMu sync.RWMutex
	reports  map[uri.URI]sema.Report
}

// NewServer returns a Server resolving configuration per view. The options
// are expected to validate; workspace settings overlay each view's config
// at initialize time and on didChangeConfiguration.
func NewServer(client protocol.Client, opts Options) *Server {
	fs := opts.Files
	if fs == nil {
		fs = vfs.NewMemoizedFS()
	}
	configSource := opts.ConfigSource
	if configSource == nil {
		configSource = FileConfigSource(fs)
	}
	version := opts.Version
	if version == "" {
		version = ServerVersion
	}
	defaults := opts.Defaults.Apply(options.Default())

	server := &Server{
		session:      store.NewSession(fs),
		client:       client,
		defaults:     defaults,
		configSource: configSource,
		version:      version,
		cli:          opts.CLI,
		configs:      make(map[uri.URI]options.Patch),
		configIssues: make(map[uri.URI]configIssue),
		reports:      make(map[uri.URI]sema.Report),
		analysis: Analysis{
			Analyzers: slices.Clone(opts.Analysis.Analyzers),
			Fixers:    slices.Clone(opts.Analysis.Fixers),
			Providers: slices.Clone(opts.Analysis.Providers),
		},
	}

	// The workspace is the only workspace: without a loader every folder
	// is one project scanned from Files, and didOpen grows it implicitly.
	loader := opts.WorkspaceLoader
	implicit := false
	if loader == nil {
		loader = defaultLoader(server.session)
		implicit = true
	}
	server.workspace = newWorkspace(server, loader)
	server.workspace.implicitFolders = implicit

	return server
}

// setWorkspaceSettings stores the workspace settings overlay; invalid
// settings are rejected and the previous document stays in effect.
func (s *Server) setWorkspaceSettings(overlay options.Patch) {
	if err := overlay.Validate(); err != nil {
		logError("workspace settings rejected", err)

		return
	}

	s.optsMu.Lock()
	s.workspaceOverlay = overlay
	s.optsMu.Unlock()

	slog.Debug("workspace settings applied")
}

func (s *Server) addProjectView(project Project) *store.View {
	cfg := s.projectViewConfig(project)
	return s.addView(project.RootURI, cfg, derefIncludePaths(cfg.IncludePaths))
}

func derefIncludePaths(p *[]string) []string {
	if p == nil {
		return nil
	}
	return *p
}

func (s *Server) addView(folder uri.URI, cfg options.Patch, includePaths []string) *store.View {
	s.applyLogLevel(cfg)

	s.cfgMu.Lock()
	s.configs[folder] = cfg
	s.cfgMu.Unlock()

	return s.session.AddView(folder, includePaths)
}

func (s *Server) removeView(folder uri.URI) {
	s.session.RemoveView(folder)

	s.cfgMu.Lock()
	delete(s.configs, folder)
	delete(s.configIssues, folder)
	s.cfgMu.Unlock()
}

// folderConfig returns the resolved configuration of a view's folder.
func (s *Server) folderConfig(folder uri.URI) options.Patch {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()

	return s.configs[folder]
}

// configIssue records an unusable thrift-ls.json for a folder.
type configIssue struct {
	// path is the file that was rejected; empty when discovery itself
	// failed.
	path string
	err  error
}

// projectViewConfig resolves the config for a loader-discovered project:
// defaults + ConfigSource document + CLI. Format and lint never come from
// the loader; include paths do. A non-empty Project.IncludePaths is
// authoritative over the file document and CLI, because the build system
// owns resolution and a stray -I must not break it. An empty list falls
// back to the file document, preserving thrift-ls.json behavior.
func (s *Server) projectViewConfig(project Project) options.Patch {
	filePatch := s.loadFilePatch(project.RootURI)
	merged := s.defaults
	if filePatch != nil {
		merged = filePatch.Apply(merged)
	}
	merged = s.cli.Apply(merged)

	if len(project.IncludePaths) > 0 {
		merged.IncludePaths = &project.IncludePaths
	}

	return merged
}

// loadFilePatch returns the ConfigSource document for root, or nil when
// none applies. Failures are recorded as config issues and yield nil, so
// callers fall back to defaults.
func (s *Server) loadFilePatch(root uri.URI) *options.Patch {
	res, err := s.configSource(root.FsPath())
	if err != nil {
		logError("config discovery failed", Expected(err), "dir", root.FsPath())
		s.recordConfigIssue(root, configIssue{path: res.Path, err: err})
		return nil
	}
	if res.Patch == nil {
		s.clearConfigIssue(root)
		return nil
	}
	if err := res.Patch.Validate(); err != nil {
		logError("config file rejected", Expected(err), "path", res.Path, "dir", root.FsPath())
		s.recordConfigIssue(root, configIssue{path: res.Path, err: err})
		return nil
	}
	s.clearConfigIssue(root)
	return res.Patch
}

// recordConfigIssue remembers and announces a rejected folder config. The
// announcement is additive: a window message for editors without a buffer
// on the JSON file, plus an error diagnostic pinned to the config file
// itself. Safe to call with any client state (nil or pre-initialized).
func (s *Server) recordConfigIssue(folder uri.URI, issue configIssue) {
	s.cfgMu.Lock()
	hadIssue := s.configIssues[folder].err != nil
	s.configIssues[folder] = issue
	s.cfgMu.Unlock()

	if hadIssue {
		// Already announced for this folder; re-resolution only happens
		// when a folder is re-added anyway.
		return
	}

	s.notifyConfigIssue(folder, issue)
}

func (s *Server) clearConfigIssue(folder uri.URI) {
	s.cfgMu.Lock()
	delete(s.configIssues, folder)
	s.cfgMu.Unlock()
}

// notifyConfigIssue pushes one warning to the client. Errors before the
// handshake finishes are dropped: notifications from an uninitialized
// server stall some clients (Helix).
func (s *Server) notifyConfigIssue(folder uri.URI, issue configIssue) {
	if s.client == nil {
		return
	}

	// Fire-and-forget: these notifications are best-effort and the
	// session must not die over a failing client pipe.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	where := "the workspace root"
	if issue.path != "" {
		where = issue.path
	}

	message := fmt.Sprintf(
		"thrift-ls: %s could not be used (%v); continuing with default settings until it is fixed",
		where, issue.err)

	err := s.client.ShowMessage(ctx, &protocol.ShowMessageParams{
		Type:    protocol.MessageTypeError,
		Message: message,
	})
	if err != nil {
		logError("config issue notification failed", err)
	}

	if issue.path == "" {
		return
	}

	diag := protocol.Diagnostic{
		Range: protocol.Range{
			Start: protocol.Position{Line: 0, Character: 0},
			End:   protocol.Position{Line: 0, Character: 0},
		},
		Severity: protocol.DiagnosticSeverityError,
		Source:   protocol.NewOptional("thrift-ls"),
		Message:  protocol.String(fmt.Sprintf("invalid configuration: %v — defaults are in effect", issue.err)),
	}

	err = s.client.PublishDiagnostics(ctx, &protocol.PublishDiagnosticsParams{
		URI:         uri.File(issue.path),
		Diagnostics: []protocol.Diagnostic{diag},
	})
	if err != nil {
		logError("config issue diagnostic failed", err)
	}
}

// applyLogLevel applies the first view config's log level; the logger is
// process-wide, so later views keep it.
func (s *Server) applyLogLevel(cfg options.Patch) {
	if cfg.LogLevel == nil {
		return
	}

	s.logLevelMu.Lock()
	defer s.logLevelMu.Unlock()

	if s.logLevel == nil {
		s.logLevel = cfg.LogLevel
		InitLogger(*cfg.LogLevel)
	}
}

// formatOptions returns the folder's config with the workspace settings
// overlay applied.
func (s *Server) formatOptions(view *store.View) formatter.Options {
	s.optsMu.RLock()
	overlay := s.workspaceOverlay
	s.optsMu.RUnlock()

	fopts, err := overlay.Apply(s.folderConfig(view.Folder())).Options()
	if err != nil {
		// Both layers were validated when stored; this is unreachable
		// unless a view config was corrupted.
		logError("formatter options rejected", err)

		fopts, _ = s.folderConfig(view.Folder()).Options()
	}

	return fopts
}

func (s *Server) Initialize(ctx context.Context, params *protocol.InitializeParams) (result *protocol.InitializeResult, err error) {
	slog.Debug("Initialize called")
	defer slog.Debug("Initialize finished")

	return s.initialize(params)
}

func (s *Server) Initialized(ctx context.Context, params *protocol.InitializedParams) (err error) {
	// The client only sends Initialized after receiving the initialize
	// response, so from here on window/logMessage notifications are safe
	// — Helix discards notifications from an uninitialized server.
	setLogClient(s.client)

	// The workspace walk and the file watcher registration run here, not
	// in initialize: the client only sends Initialized after receiving
	// the initialize response, so nothing the server emits at this
	// point races the handshake. Sending the registerCapability request
	// or diagnostics any earlier violates the spec — Helix deadlocks on
	// a client request that arrives before initialize is answered.
	s.workspaceWalkOnce.Do(func() {
		s.workspace.start()
	})

	s.registerFileWatcher(ctx)

	return nil
}

func (s *Server) Shutdown(ctx context.Context) (err error) {
	s.workspace.shutdown()

	return nil
}

func (s *Server) Exit(ctx context.Context) (err error) {
	return nil
}

func (s *Server) WorkDoneProgressCancel(ctx context.Context, params *protocol.WorkDoneProgressCancelParams) (err error) {
	return nil
}

func (s *Server) LogTrace(ctx context.Context, params *protocol.LogTraceParams) (err error) {
	return nil
}

func (s *Server) SetTrace(ctx context.Context, params *protocol.SetTraceParams) (err error) {
	return nil
}

func (s *Server) CodeAction(ctx context.Context, params *protocol.CodeActionParams) (result []protocol.CommandOrCodeAction, err error) {
	return s.codeAction(ctx, params)
}

func (s *Server) CodeLens(ctx context.Context, params *protocol.CodeLensParams) (result []protocol.CodeLens, err error) {
	return []protocol.CodeLens{}, nil
}

func (s *Server) CodeLensResolve(ctx context.Context, params *protocol.CodeLens) (result *protocol.CodeLens, err error) {
	return nil, nil
}

func (s *Server) ColorPresentation(ctx context.Context, params *protocol.ColorPresentationParams) (result []protocol.ColorPresentation, err error) {
	return []protocol.ColorPresentation{}, nil
}

func (s *Server) Completion(ctx context.Context, params *protocol.CompletionParams) (result protocol.CompletionResult, err error) {
	slog.Debug("Completion called")
	defer slog.Debug("Completion finished")

	return s.completion(ctx, params)
}

func (s *Server) CompletionResolve(ctx context.Context, params *protocol.CompletionItem) (result *protocol.CompletionItem, err error) {
	return params, nil
}

func (s *Server) Declaration(ctx context.Context, params *protocol.DeclarationParams) (result protocol.DeclarationResult, err error) {
	// Thrift has no separate declaration concept: a declaration is the
	// definition.
	res, err := s.definition(ctx, &protocol.DefinitionParams{
		TextDocumentPositionParams: params.TextDocumentPositionParams,
	})
	if err != nil {
		return nil, err
	}

	return protocol.LocationSlice(res), nil
}

func (s *Server) Definition(ctx context.Context, params *protocol.DefinitionParams) (result protocol.DefinitionResult, err error) {
	slog.Debug("Definition called")
	defer slog.Debug("Definition finished")

	res, err := s.definition(ctx, params)

	return protocol.LocationSlice(res), err
}

func (s *Server) DidChange(ctx context.Context, params *protocol.DidChangeTextDocumentParams) (err error) {
	slog.Debug("DidChange called")
	defer slog.Debug("DidChange finished")

	return s.didChange(ctx, params)
}

func (s *Server) DidChangeConfiguration(ctx context.Context, params *protocol.DidChangeConfigurationParams) (err error) {
	if len(params.Settings) == 0 {
		return nil
	}

	patch, err := lspSettings(params.Settings)
	if err != nil {
		logError("didChangeConfiguration rejected", err)
		return nil
	}

	s.setWorkspaceSettings(*patch)

	return nil
}

func (s *Server) DidChangeWatchedFiles(ctx context.Context, params *protocol.DidChangeWatchedFilesParams) (err error) {
	return s.didChangeWatchedFiles(ctx, params)
}

func (s *Server) DidChangeWorkspaceFolders(ctx context.Context, params *protocol.DidChangeWorkspaceFoldersParams) (err error) {
	added := make([]uri.URI, len(params.Event.Added))
	for i, folder := range params.Event.Added {
		added[i] = folder.URI
	}
	removed := make([]uri.URI, len(params.Event.Removed))
	for i, folder := range params.Event.Removed {
		removed[i] = folder.URI
	}

	s.workspace.changeFolders(added, removed)

	return nil
}

func (s *Server) DidClose(ctx context.Context, params *protocol.DidCloseTextDocumentParams) (err error) {
	return s.didClose(ctx, params)
}

func (s *Server) DidOpen(ctx context.Context, params *protocol.DidOpenTextDocumentParams) (err error) {
	slog.Debug("DidOpen called")
	defer slog.Debug("DidOpen finished")

	return s.didOpen(ctx, params)
}

func (s *Server) DidSave(ctx context.Context, params *protocol.DidSaveTextDocumentParams) (err error) {
	return nil
}

func (s *Server) DocumentColor(ctx context.Context, params *protocol.DocumentColorParams) (result []protocol.ColorInformation, err error) {
	return []protocol.ColorInformation{}, nil
}

func (s *Server) DocumentHighlight(ctx context.Context, params *protocol.DocumentHighlightParams) (result []protocol.DocumentHighlight, err error) {
	return s.documentHighlight(ctx, params)
}

func (s *Server) DocumentLink(ctx context.Context, params *protocol.DocumentLinkParams) (result []protocol.DocumentLink, err error) {
	return s.documentLink(ctx, params)
}

func (s *Server) DocumentLinkResolve(ctx context.Context, params *protocol.DocumentLink) (result *protocol.DocumentLink, err error) {
	return nil, nil
}

func (s *Server) DocumentSymbol(ctx context.Context, params *protocol.DocumentSymbolParams) (result protocol.DocumentSymbolResult, err error) {
	slog.Debug("DocumentSymbol called")
	defer slog.Debug("DocumentSymbol finished")

	return s.documentSymbol(ctx, params)
}

func (s *Server) ExecuteCommand(ctx context.Context, params *protocol.ExecuteCommandParams) (result protocol.LSPAny, err error) {
	return nil, nil
}

func (s *Server) FoldingRanges(ctx context.Context, params *protocol.FoldingRangeParams) (result []protocol.FoldingRange, err error) {
	return s.foldingRanges(ctx, params)
}

func (s *Server) Formatting(ctx context.Context, params *protocol.DocumentFormattingParams) (result []protocol.TextEdit, err error) {
	slog.Debug("Formatting called")
	defer slog.Debug("Formatting finished")

	return s.formatting(ctx, params)
}

func (s *Server) Hover(ctx context.Context, params *protocol.HoverParams) (result *protocol.Hover, err error) {
	slog.Debug("hover called")
	defer slog.Debug("hover finished")

	return s.hover(ctx, params)
}

func (s *Server) Implementation(ctx context.Context, params *protocol.ImplementationParams) (result protocol.DefinitionResult, err error) {
	return protocol.LocationSlice{}, nil
}

func (s *Server) OnTypeFormatting(ctx context.Context, params *protocol.DocumentOnTypeFormattingParams) (result []protocol.TextEdit, err error) {
	return withFile(ctx, s.viewOf, params.TextDocument.URI, func(view *store.View, fh vfs.FileHandle) ([]protocol.TextEdit, error) {
		return source.OnTypeFormat(ctx, view, fh, s.formatOptions(view), params.Position)
	})
}

func (s *Server) PrepareRename(ctx context.Context, params *protocol.PrepareRenameParams) (result protocol.PrepareRenameResult, err error) {
	slog.Debug("PrepareRename called")
	defer slog.Debug("PrepareRename finished")

	return s.prepareRename(ctx, params)
}

func (s *Server) RangeFormatting(ctx context.Context, params *protocol.DocumentRangeFormattingParams) (result []protocol.TextEdit, err error) {
	return s.rangeFormatting(ctx, params)
}

func (s *Server) References(ctx context.Context, params *protocol.ReferenceParams) (result []protocol.Location, err error) {
	slog.Debug("References called")
	defer slog.Debug("References finished")

	return s.references(ctx, params)
}

func (s *Server) Rename(ctx context.Context, params *protocol.RenameParams) (result *protocol.WorkspaceEdit, err error) {
	slog.Debug("Rename called")
	defer slog.Debug("Rename finished")

	return s.rename(ctx, params)
}

func (s *Server) SignatureHelp(ctx context.Context, params *protocol.SignatureHelpParams) (result *protocol.SignatureHelp, err error) {
	return nil, nil
}

func (s *Server) Symbols(ctx context.Context, params *protocol.WorkspaceSymbolParams) (result protocol.WorkspaceSymbolResult, err error) {
	views := s.session.Views()
	slices.SortFunc(views, func(a, b *store.View) int {
		return strings.Compare(string(a.Folder()), string(b.Folder()))
	})

	const maxResults = 1000

	var res []protocol.SymbolInformation

	for _, view := range views {
		files := s.workspace.files(view)

		syms := source.WorkspaceSymbols(ctx, view, files, params.Query, maxResults-len(res))

		res = append(res, syms...)
		if len(res) >= maxResults {
			break
		}
	}

	return protocol.SymbolInformationSlice(res), nil
}

func (s *Server) TypeDefinition(ctx context.Context, params *protocol.TypeDefinitionParams) (result protocol.DefinitionResult, err error) {
	slog.Debug("TypeDefinition called")
	defer slog.Debug("TypeDefinition finished")

	res, err := s.typeDefinition(ctx, params)

	return protocol.LocationSlice(res), err
}

func (s *Server) WillSave(ctx context.Context, params *protocol.WillSaveTextDocumentParams) (err error) {
	return nil
}

func (s *Server) WillSaveWaitUntil(ctx context.Context, params *protocol.WillSaveTextDocumentParams) (result []protocol.TextEdit, err error) {
	return []protocol.TextEdit{}, nil
}

func (s *Server) ShowDocument(ctx context.Context, params *protocol.ShowDocumentParams) (result *protocol.ShowDocumentResult, err error) {
	return nil, nil
}

func (s *Server) WillCreateFiles(ctx context.Context, params *protocol.CreateFilesParams) (result *protocol.WorkspaceEdit, err error) {
	return nil, nil
}

func (s *Server) DidCreateFiles(ctx context.Context, params *protocol.CreateFilesParams) (err error) {
	return nil
}

func (s *Server) WillRenameFiles(ctx context.Context, params *protocol.RenameFilesParams) (result *protocol.WorkspaceEdit, err error) {
	return s.willRenameFiles(ctx, params)
}

func (s *Server) DidRenameFiles(ctx context.Context, params *protocol.RenameFilesParams) (err error) {
	return s.didRenameFiles(ctx, params)
}

func (s *Server) WillDeleteFiles(ctx context.Context, params *protocol.DeleteFilesParams) (result *protocol.WorkspaceEdit, err error) {
	return nil, nil
}

func (s *Server) DidDeleteFiles(ctx context.Context, params *protocol.DeleteFilesParams) (err error) {
	return nil
}

func (s *Server) CodeLensRefresh(ctx context.Context) (err error) {
	return nil
}

func (s *Server) PrepareCallHierarchy(ctx context.Context, params *protocol.CallHierarchyPrepareParams) (result []protocol.CallHierarchyItem, err error) {
	return []protocol.CallHierarchyItem{}, nil
}

func (s *Server) IncomingCalls(ctx context.Context, params *protocol.CallHierarchyIncomingCallsParams) (result []protocol.CallHierarchyIncomingCall, err error) {
	return []protocol.CallHierarchyIncomingCall{}, nil
}

func (s *Server) OutgoingCalls(ctx context.Context, params *protocol.CallHierarchyOutgoingCallsParams) (result []protocol.CallHierarchyOutgoingCall, err error) {
	return []protocol.CallHierarchyOutgoingCall{}, nil
}

func (s *Server) SemanticTokensFull(ctx context.Context, params *protocol.SemanticTokensParams) (result *protocol.SemanticTokens, err error) {
	return s.semanticTokensFull(ctx, params)
}

func (s *Server) SemanticTokensFullDelta(ctx context.Context, params *protocol.SemanticTokensDeltaParams) (result protocol.SemanticTokensDeltaResult, err error) {
	// No delta tracking: answer every request with the full token set,
	// which is a valid delta response.
	tokens, err := s.semanticTokensFull(ctx, &protocol.SemanticTokensParams{
		TextDocument: params.TextDocument,
	})
	if err != nil {
		return nil, err
	}

	return tokens, nil
}

func (s *Server) SemanticTokensRange(ctx context.Context, params *protocol.SemanticTokensRangeParams) (result *protocol.SemanticTokens, err error) {
	return nil, nil
}

func (s *Server) SemanticTokensRefresh(ctx context.Context) (err error) {
	return nil
}

func (s *Server) LinkedEditingRange(ctx context.Context, params *protocol.LinkedEditingRangeParams) (result *protocol.LinkedEditingRanges, err error) {
	return nil, nil
}

func (s *Server) Moniker(ctx context.Context, params *protocol.MonikerParams) (result []protocol.Moniker, err error) {
	return []protocol.Moniker{}, nil
}

// Request handles all no standard request
func (s *Server) Request(ctx context.Context, method string, params any) (result any, err error) {
	return nil, nil
}

// The following methods satisfy the full protocol.Server interface. thrift-ls
// does not implement these features; they return empty results.

func (s *Server) Progress(ctx context.Context, params *protocol.ProgressParams) error {
	return nil
}

func (s *Server) DidOpenNotebookDocument(ctx context.Context, params *protocol.DidOpenNotebookDocumentParams) error {
	return nil
}

func (s *Server) DidChangeNotebookDocument(ctx context.Context, params *protocol.DidChangeNotebookDocumentParams) error {
	return nil
}

func (s *Server) DidSaveNotebookDocument(ctx context.Context, params *protocol.DidSaveNotebookDocumentParams) error {
	return nil
}

func (s *Server) DidCloseNotebookDocument(ctx context.Context, params *protocol.DidCloseNotebookDocumentParams) error {
	return nil
}

func (s *Server) PrepareTypeHierarchy(ctx context.Context, params *protocol.TypeHierarchyPrepareParams) ([]protocol.TypeHierarchyItem, error) {
	return nil, nil
}

func (s *Server) Supertypes(ctx context.Context, params *protocol.TypeHierarchySupertypesParams) ([]protocol.TypeHierarchyItem, error) {
	return nil, nil
}

func (s *Server) Subtypes(ctx context.Context, params *protocol.TypeHierarchySubtypesParams) ([]protocol.TypeHierarchyItem, error) {
	return nil, nil
}

func (s *Server) SelectionRange(ctx context.Context, params *protocol.SelectionRangeParams) ([]protocol.SelectionRange, error) {
	return nil, nil
}

func (s *Server) InlineValue(ctx context.Context, params *protocol.InlineValueParams) ([]protocol.InlineValue, error) {
	return nil, nil
}

func (s *Server) InlayHint(ctx context.Context, params *protocol.InlayHintParams) ([]protocol.InlayHint, error) {
	return nil, nil
}

func (s *Server) InlayHintResolve(ctx context.Context, params *protocol.InlayHint) (*protocol.InlayHint, error) {
	return params, nil
}

func (s *Server) Diagnostic(ctx context.Context, params *protocol.DocumentDiagnosticParams) (protocol.DocumentDiagnosticReport, error) {
	return nil, nil
}

func (s *Server) DiagnosticWorkspace(ctx context.Context, params *protocol.WorkspaceDiagnosticParams) (*protocol.WorkspaceDiagnosticReport, error) {
	return nil, nil
}

func (s *Server) CodeActionResolve(ctx context.Context, params *protocol.CodeAction) (*protocol.CodeAction, error) {
	return params, nil
}

func (s *Server) RangesFormatting(ctx context.Context, params *protocol.DocumentRangesFormattingParams) ([]protocol.TextEdit, error) {
	return nil, nil
}

func (s *Server) InlineCompletion(ctx context.Context, params *protocol.InlineCompletionParams) (protocol.InlineCompletionResult, error) {
	return nil, nil
}

func (s *Server) WorkspaceSymbolResolve(ctx context.Context, params *protocol.WorkspaceSymbol) (*protocol.WorkspaceSymbol, error) {
	return params, nil
}

func (s *Server) TextDocumentContent(ctx context.Context, params *protocol.TextDocumentContentParams) (*protocol.TextDocumentContentResult, error) {
	return nil, nil
}
