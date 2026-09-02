package lsp

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/source"
)

func (s *Server) initialize(params *protocol.InitializeParams) (result *protocol.InitializeResult, err error) {
	// Prefer WorkspaceFolders; fall back to the deprecated RootURI/RootPath
	// fields for older clients.
	folders := make([]uri.URI, 0, 1)
	if wsf, ok := params.WorkspaceFolders.Get(); ok {
		folders = make([]uri.URI, 0, len(wsf))
		for _, ws := range wsf {
			folders = append(folders, ws.URI)
		}
	}

	if len(folders) == 0 {
		//nolint:staticcheck // intentional handling of legacy client params
		rootURI := params.RootURI
		if rootURI == nil {
			//nolint:staticcheck // intentional handling of legacy client params
			if rootPath, ok := params.RootPath.Get(); ok {
				r := uri.URI(rootPath)
				rootURI = &r
			}
		}

		if rootURI != nil {
			folders = append(folders, *rootURI)
		}
	}

	slog.Debug("initialized folders", "folders", folders)

	if s.workspace != nil {
		s.workspace.initialize(folders)
	} else {
		s.folders = folders
	}

	// Workspace settings (initializationOptions) overlay each view's
	// config; didChangeConfiguration updates them later.
	if len(params.InitializationOptions) > 0 {
		if patch, err := lspSettings(params.InitializationOptions); err != nil {
			logError("initializationOptions rejected", err)
		} else {
			s.setWorkspaceSettings(*patch)
		}
	}

	// The workspace walk and the file watcher registration run on the
	// Initialized notification, not here: the spec forbids the server from
	// sending requests or notifications to the client before responding to
	// initialize. Helix deadlocks on the registerCapability request during
	// the handshake and discards (or stalls on) notifications from an
	// uninitialized server.
	return initializeResult(s.version), nil
}

// registerFileWatcher subscribes the client to disk events for thrift files,
// so files created or changed outside the editor (git pull, a terminal)
// reach the server and re-run diagnostics for their dependents. Without the
// registration, a new include created on disk is invisible until the editor
// opens it.
func (s *Server) registerFileWatcher(ctx context.Context) {
	if s.client == nil {
		return
	}

	options, err := json.Marshal(protocol.DidChangeWatchedFilesRegistrationOptions{
		Watchers: []protocol.FileSystemWatcher{
			{
				GlobPattern: protocol.Pattern("**/*.thrift"),
				Kind:        protocol.WatchKindCreate | protocol.WatchKindChange | protocol.WatchKindDelete,
			},
		},
	})
	if err != nil {
		slog.Error("file watcher registration failed", "err", err)

		return
	}

	err = s.client.RegisterCapability(ctx, &protocol.RegistrationParams{
		Registrations: []protocol.Registration{
			{
				ID:              "thrift-ls.watcher",
				Method:          protocol.MethodWorkspaceDidChangeWatchedFiles,
				RegisterOptions: protocol.LSPAny(options),
			},
		},
	})
	if err != nil {
		slog.Error("file watcher registration failed", "err", err)

		return
	}

	slog.Debug("file watcher registered")
}

func (s *Server) walkFoldersThriftFile(folder uri.URI) {
	slog.Debug("walk dir", "folder", folder.Path())

	// The view is the folder itself, so files in nested directories
	// resolve to it via ContainsFile; addFolderView resolves its config.
	view := s.addFolderView(folder)
	migrated := s.reconcileStockAddition(context.TODO(), view)

	// Walk the folder through the session's file source: the disk in
	// production, an in-memory tree in tests. WalkDir walks with lexical
	// order; the fs implementations handle their own entry errors.
	changes := s.workspaceFileChanges([]uri.URI{folder})
	seen := make(map[uri.URI]struct{}, len(changes))
	for _, change := range changes {
		seen[change.URI] = struct{}{}
	}

	for _, fileURI := range migrated {
		if _, ok := seen[fileURI]; ok {
			continue
		}

		changes = append(changes, &cache.FileChange{
			URI:  fileURI,
			From: cache.FileChangeTypeInitialize,
		})
	}

	if err := s.applyChanges(context.TODO(), changes, false); err != nil {
		slog.Warn("workspace files failed", "err", err)
	}
}

func (s *Server) walkWorkspaceFolders(folders []uri.URI) {
	var migrated []uri.URI
	for _, folder := range folders {
		view := s.addFolderView(folder)
		migrated = append(migrated, s.reconcileStockAddition(context.TODO(), view)...)
	}

	changes := s.workspaceFileChanges(folders)
	seen := make(map[uri.URI]struct{}, len(changes))
	for _, change := range changes {
		seen[change.URI] = struct{}{}
	}

	for _, fileURI := range migrated {
		if _, ok := seen[fileURI]; ok {
			continue
		}

		changes = append(changes, &cache.FileChange{
			URI:  fileURI,
			From: cache.FileChangeTypeInitialize,
		})
	}

	if err := s.applyChanges(context.TODO(), changes, false); err != nil {
		slog.Warn("workspace files failed", "err", err)
	}
}

func (s *Server) workspaceFileChanges(folders []uri.URI) []*cache.FileChange {
	var changes []*cache.FileChange
	seen := make(map[uri.URI]struct{})

	for _, folder := range folders {
		_ = s.session.WalkFiles(context.TODO(), folder, func(fileURI uri.URI) error {
			if !strings.HasSuffix(fileURI.Path(), ".thrift") {
				return nil
			}

			if _, ok := seen[fileURI]; ok {
				return nil
			}
			seen[fileURI] = struct{}{}

			slog.Debug("file path", "uri", fileURI)
			changes = append(changes, &cache.FileChange{
				URI:     fileURI,
				Version: 0,
				Content: []byte{},
				From:    cache.FileChangeTypeInitialize,
			})

			return nil
		})
	}

	return changes
}

func (s *Server) reconcileStockAddition(ctx context.Context, view *cache.View) []uri.URI {
	var migrated []uri.URI
	seen := make(map[uri.URI]struct{})

	for _, other := range s.session.Views() {
		if other == view || !other.ContainsFile(view.Folder()) {
			continue
		}

		var evicted []uri.URI
		for _, fileURI := range other.KnownFiles() {
			if !view.ContainsFile(fileURI) {
				continue
			}

			evicted = append(evicted, fileURI)
			if _, ok := seen[fileURI]; !ok {
				seen[fileURI] = struct{}{}
				migrated = append(migrated, fileURI)
			}
		}

		if len(evicted) == 0 {
			continue
		}

		other.Evict(evicted...)
		s.clearDiagnostics(ctx, evicted...)
	}

	return migrated
}

func (s *Server) removeStockView(ctx context.Context, folder uri.URI) {
	var removed *cache.View
	for _, view := range s.session.Views() {
		if view.Folder() == folder {
			removed = view

			break
		}
	}
	if removed == nil {
		s.removeView(folder)

		return
	}

	files := removed.KnownFiles()
	s.removeView(folder)
	s.clearDiagnostics(ctx, files...)

	byView := make(map[*cache.View][]uri.URI)
	for _, fileURI := range files {
		view := s.stockViewOf(fileURI)
		if view == nil {
			continue
		}

		byView[view] = append(byView[view], fileURI)
	}

	for view, files := range byView {
		updates := make([]*cache.FileChange, len(files))
		for i, fileURI := range files {
			updates[i] = &cache.FileChange{URI: fileURI, From: cache.FileChangeTypeInitialize}
		}

		s.postDiagnostics(ctx, view, view.Update(ctx, updates...))
	}
}

func (s *Server) stockViewOf(fileURI uri.URI) *cache.View {
	var best *cache.View
	for _, view := range s.session.Views() {
		if !view.ContainsFile(fileURI) {
			continue
		}
		if best == nil || len(view.Folder().Path()) > len(best.Folder().Path()) {
			best = view
		}
	}
	if best != nil {
		return best
	}

	for _, view := range s.session.Views() {
		if view.FileKnown(fileURI) {
			return view
		}
	}

	return nil
}

// thriftFileOperationFilters is the registration for one file operation:
// every thrift file, files only.
func thriftFileOperationFilters() protocol.FileOperationRegistrationOptions {
	return protocol.FileOperationRegistrationOptions{
		Filters: []protocol.FileOperationFilter{
			{
				Scheme: new("file"),
				Pattern: protocol.FileOperationPattern{
					Glob:    "**/*.thrift",
					Matches: protocol.FileOperationPatternKindFile,
				},
			},
		},
	}
}

func initializeResult(version string) *protocol.InitializeResult {
	thriftSelector := &protocol.DocumentSelector{
		&protocol.TextDocumentFilterLanguage{Language: LanguageIDThrift},
	}

	return &protocol.InitializeResult{
		Capabilities: protocol.ServerCapabilities{
			TextDocumentSync: &protocol.TextDocumentSyncOptions{
				OpenClose: new(true),
				// full is easy to implement. consider to use incremental for performance
				Change: new(protocol.TextDocumentSyncKindFull),
			},
			CompletionProvider: &protocol.CompletionOptions{
				ResolveProvider: new(false),
				/**
				 * The additional characters, beyond the defaults provided by the client (typically
				 * [a-zA-Z]), that should automatically trigger a completion request. For example
				 * `.` in JavaScript represents the beginning of an object property or method and is
				 * thus a good candidate for triggering a completion request.
				 *
				 * Most tools trigger a completion request automatically without explicitly
				 * requesting it using a keyboard shortcut (e.g. Ctrl+Space). Typically they
				 * do so when the user starts to type an identifier. For example if the user
				 * types `c` in a JavaScript file code complete will automatically pop up
				 * present `console` besides others as a completion item. Characters that
				 * make up identifiers don't need to be listed here.
				 */
				// "." for enum-qualified values (Color.|), "\"" for include
				// path literals, "(" for annotation keys.
				TriggerCharacters: []string{".", "\"", "("},
			},
			HoverProvider: &protocol.HoverOptions{
				WorkDoneProgressOptions: protocol.WorkDoneProgressOptions{
					WorkDoneProgress: new(true),
				},
			},
			DeclarationProvider: &protocol.DeclarationRegistrationOptions{
				DeclarationOptions: protocol.DeclarationOptions{
					WorkDoneProgressOptions: protocol.WorkDoneProgressOptions{
						WorkDoneProgress: new(true),
					},
				},
				TextDocumentRegistrationOptions: protocol.TextDocumentRegistrationOptions{
					DocumentSelector: thriftSelector,
				},
				StaticRegistrationOptions: protocol.StaticRegistrationOptions{
					ID: new("thrift-ls"),
				},
			},
			DefinitionProvider: &protocol.DefinitionOptions{
				WorkDoneProgressOptions: protocol.WorkDoneProgressOptions{
					WorkDoneProgress: new(true),
				},
			},
			TypeDefinitionProvider: &protocol.TypeDefinitionRegistrationOptions{
				TextDocumentRegistrationOptions: protocol.TextDocumentRegistrationOptions{
					DocumentSelector: thriftSelector,
				},
				TypeDefinitionOptions: protocol.TypeDefinitionOptions{
					WorkDoneProgressOptions: protocol.WorkDoneProgressOptions{
						WorkDoneProgress: new(true),
					},
				},
				StaticRegistrationOptions: protocol.StaticRegistrationOptions{
					ID: new("thrift-ls"),
				},
			},
			ReferencesProvider: &protocol.ReferenceOptions{
				WorkDoneProgressOptions: protocol.WorkDoneProgressOptions{
					WorkDoneProgress: new(true),
				},
			},
			DocumentHighlightProvider: protocol.Boolean(true),
			DocumentSymbolProvider: &protocol.DocumentSymbolOptions{
				WorkDoneProgressOptions: protocol.WorkDoneProgressOptions{
					WorkDoneProgress: new(true),
				},
				Label: new("thrift-ls"),
			},
			CodeActionProvider: &protocol.CodeActionOptions{
				// Keep in sync with the kinds codeAction returns:
				// quickfix (fixes for reported diagnostics) and
				// refactor.rewrite (rewrites at the selection).
				CodeActionKinds: []protocol.CodeActionKind{
					protocol.CodeActionKindQuickFix,
					protocol.CodeActionKindRefactorRewrite,
				},
				ResolveProvider: new(false),
			},
			DocumentLinkProvider: &protocol.DocumentLinkOptions{
				ResolveProvider: new(false),
			},
			ColorProvider:        protocol.Boolean(false),
			FoldingRangeProvider: protocol.Boolean(true),
			WorkspaceSymbolProvider: &protocol.WorkspaceSymbolOptions{
				WorkDoneProgressOptions: protocol.WorkDoneProgressOptions{
					WorkDoneProgress: new(true),
				},
			},
			DocumentFormattingProvider: &protocol.DocumentFormattingOptions{
				WorkDoneProgressOptions: protocol.WorkDoneProgressOptions{
					WorkDoneProgress: new(true),
				},
			},
			DocumentRangeFormattingProvider: &protocol.DocumentRangeFormattingOptions{
				WorkDoneProgressOptions: protocol.WorkDoneProgressOptions{
					WorkDoneProgress: new(true),
				},
			},
			DocumentOnTypeFormattingProvider: protocol.DocumentOnTypeFormattingOptions{
				FirstTriggerCharacter: "}",
				MoreTriggerCharacter:  []string{},
			},
			RenameProvider: &protocol.RenameOptions{
				PrepareProvider: new(true),
			},
			CallHierarchyProvider:      protocol.Boolean(false),
			LinkedEditingRangeProvider: protocol.Boolean(false),
			SemanticTokensProvider: &protocol.SemanticTokensRegistrationOptions{
				TextDocumentRegistrationOptions: protocol.TextDocumentRegistrationOptions{
					DocumentSelector: thriftSelector,
				},
				SemanticTokensOptions: protocol.SemanticTokensOptions{
					WorkDoneProgressOptions: protocol.WorkDoneProgressOptions{
						WorkDoneProgress: new(true),
					},
					Legend: protocol.SemanticTokensLegend{
						TokenTypes:     source.Legend(),
						TokenModifiers: []string{},
					},
					Full: &protocol.SemanticTokensFullDelta{
						Delta: new(true),
					},
					Range: protocol.Boolean(false),
				},
				StaticRegistrationOptions: protocol.StaticRegistrationOptions{
					ID: new("thrift-ls"),
				},
			},
			Workspace: &protocol.WorkspaceOptions{
				WorkspaceFolders: &protocol.WorkspaceFoldersServerCapabilities{
					Supported:           new(true),
					ChangeNotifications: protocol.Boolean(true),
				},
				// Renaming a thrift file must rewrite the include literals
				// of its dependents; create and delete need no text edits
				// and stay unadvertised.
				FileOperations: &protocol.FileOperationOptions{
					DidRename:  thriftFileOperationFilters(),
					WillRename: thriftFileOperationFilters(),
				},
			},
			MonikerProvider: nil,
			Experimental:    nil,
		},
		ServerInfo: protocol.ServerInfo{
			Name:    ServerName,
			Version: protocol.NewOptional(version),
		},
	}
}
