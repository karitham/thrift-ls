package lsp

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/formatter"
	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/source"
	"github.com/karitham/thrift-ls/options"
)

type Server struct {
	cache   *cache.Cache
	session *cache.Session

	client protocol.Client

	// explicit is the startup configuration (defaults + startup config +
	// CLI); every view uses it when configPath pins a file, otherwise each
	// view resolves its own config from its folder.
	explicit   options.Patch
	configPath string

	// cli is the CLI-only overlay, applied on top of every view's config.
	cli options.Patch

	// workspaceOverlay is the last accepted workspace settings, overlaid
	// on every view's config. Guarded by optsMu.
	optsMu           sync.RWMutex
	workspaceOverlay options.Patch

	// logLevel is the first view config's log level, applied once the
	// workspace is known. Guarded by logLevelMu.
	logLevelMu sync.Mutex
	logLevel   *int

	// folders are the workspace folders from the initialize request; the
	// walk starts on the Initialized notification so the initialize
	// handshake never blocks on parsing the workspace.
	folders []uri.URI

	// workspaceWalkOnce and dirWalkOnce guard the two independent walks:
	// the whole workspace on Initialized, and the opened file's directory
	// on the first didOpen (single-file mode). They must not share a guard
	// — a didOpen racing the Initialized notification would otherwise
	// permanently skip the workspace walk.
	workspaceWalkOnce sync.Once
	dirWalkOnce       sync.Once
}

// NewServer returns a Server resolving configuration per view. The options
// are expected to validate; workspace settings overlay each view's config
// at initialize time and on didChangeConfiguration.
func NewServer(c *cache.Cache, client protocol.Client, opts Options) *Server {
	return &Server{
		cache:      c,
		session:    cache.NewSession(c),
		client:     client,
		explicit:   opts.Config,
		configPath: opts.ConfigPath,
		cli:        opts.CLI,
	}
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

// addFolderView creates the view for a workspace folder, resolving the
// folder's config at creation — when the workspace is finally known.
func (s *Server) addFolderView(folder uri.URI) *cache.View {
	cfg := s.viewConfig(folder)
	s.applyLogLevel(cfg)

	var includePaths []string
	if cfg.IncludePaths != nil {
		includePaths = *cfg.IncludePaths
	}

	return s.session.AddView(folder, includePaths, cfg)
}

// viewConfig resolves the config for a view rooted at folder: the pinned
// --config file, or the nearest thrift-ls.json walking up, plus CLI flags.
// A folder with no usable config formats with defaults.
func (s *Server) viewConfig(folder uri.URI) options.Patch {
	if s.configPath != "" {
		return s.cli.Apply(s.explicit)
	}

	cfgPath, err := options.FindConfig(folder.FsPath())
	if err != nil {
		logError("config discovery failed", Expected(err), "dir", folder.FsPath())

		return s.defaultConfig()
	}

	if cfgPath == "" {
		return s.defaultConfig()
	}

	cfg, err := options.Load(cfgPath)
	if err != nil {
		logError("config file rejected", Expected(err), "path", cfgPath)

		return s.defaultConfig()
	}

	return s.cli.Apply(options.Effective(cfg))
}

// defaultConfig is the fallback for a folder without a usable config
// file: the defaults with the CLI overlay.
func (s *Server) defaultConfig() options.Patch {
	return s.cli.Apply(options.Default())
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

// formatOptions returns the view's config with the workspace settings
// overlay applied.
func (s *Server) formatOptions(view *cache.View) formatter.Options {
	s.optsMu.RLock()
	overlay := s.workspaceOverlay
	s.optsMu.RUnlock()

	fopts, err := formatter.FromConfig(overlay.Apply(view.Config()))
	if err != nil {
		// Both layers were validated when stored; this is unreachable
		// unless a view config was corrupted.
		logError("formatter options rejected", err)

		fopts, _ = formatter.FromConfig(view.Config())
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
		go func() {
			for _, folder := range s.folders {
				s.walkFoldersThriftFile(folder)
			}
		}()
	})

	s.registerFileWatcher(ctx)

	return nil
}

func (s *Server) Shutdown(ctx context.Context) (err error) {
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
	for _, folder := range params.Event.Removed {
		s.session.RemoveView(folder.URI)
	}

	for _, folder := range params.Event.Added {
		s.walkFoldersThriftFile(folder.URI)
	}

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
	return withFile(ctx, s.session, params.TextDocument.URI, func(view *cache.View, fh cache.FileHandle) ([]protocol.TextEdit, error) {
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
	slices.SortFunc(views, func(a, b *cache.View) int {
		return strings.Compare(string(a.Folder()), string(b.Folder()))
	})

	const maxResults = 1000

	var res []protocol.SymbolInformation

	for _, view := range views {
		syms := source.WorkspaceSymbols(ctx, view, view.KnownFiles(), params.Query, maxResults-len(res))

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
	return nil, nil
}

func (s *Server) DidRenameFiles(ctx context.Context, params *protocol.RenameFilesParams) (err error) {
	return nil
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
