package lsp

import (
	"context"
	"log/slog"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/formatter"
	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/symbols"
)

type Server struct {
	cache   *cache.Cache
	session *cache.Session

	client     protocol.Client
	formatOpts formatter.Options
}

func NewServer(c *cache.Cache, client protocol.Client, formatOpts formatter.Options) *Server {
	return &Server{
		cache:      c,
		session:    cache.NewSession(c),
		client:     client,
		formatOpts: formatOpts,
	}
}

func (s *Server) Initialize(ctx context.Context, params *protocol.InitializeParams) (result *protocol.InitializeResult, err error) {
	slog.Debug("Initialize called")
	defer slog.Debug("Initialize finished")

	return s.initialize(ctx, params)
}

func (s *Server) Initialized(ctx context.Context, params *protocol.InitializedParams) (err error) {
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
	return []protocol.CommandOrCodeAction{}, nil
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
	return protocol.LocationSlice{}, nil
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
		s.session.AddView(folder.URI)
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
	return []protocol.DocumentHighlight{}, nil
}

func (s *Server) DocumentLink(ctx context.Context, params *protocol.DocumentLinkParams) (result []protocol.DocumentLink, err error) {
	return []protocol.DocumentLink{}, nil
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
	return []protocol.TextEdit{}, nil
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
	return protocol.SymbolInformationSlice(symbols.WorkspaceSymbols(ctx, s.session, params.Query, 1000)), nil
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
	return nil, nil
}

func (s *Server) SemanticTokensFullDelta(ctx context.Context, params *protocol.SemanticTokensDeltaParams) (result protocol.SemanticTokensDeltaResult, err error) {
	return nil, nil
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
