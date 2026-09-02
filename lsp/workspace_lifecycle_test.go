package lsp

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
)

func TestCustomDocumentChangesDuringWorkspaceLoad(t *testing.T) {
	tests := []struct {
		name        string
		afterOpen   func(context.Context, *Server, uri.URI) error
		wantDef     string
		wantOverlay bool
	}{
		{
			name: "open then change",
			afterOpen: func(ctx context.Context, srv *Server, file uri.URI) error {
				return srv.DidChange(ctx, &protocol.DidChangeTextDocumentParams{
					TextDocument: protocol.VersionedTextDocumentIdentifier{
						TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: file},
						Version:                1,
					},
					ContentChanges: []protocol.TextDocumentContentChangeEvent{
						&protocol.TextDocumentContentChangeWholeDocument{Text: "struct ChangedVersion {}"},
					},
				})
			},
			wantDef:     "ChangedVersion",
			wantOverlay: true,
		},
		{
			name: "open then close",
			afterOpen: func(ctx context.Context, srv *Server, file uri.URI) error {
				return srv.DidClose(ctx, &protocol.DidCloseTextDocumentParams{
					TextDocument: protocol.TextDocumentIdentifier{URI: file},
				})
			},
			wantDef: "DiskVersion",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				workspace := uri.File("/workspace")
				file := uri.File("/workspace/api.thrift")
				started := make(chan struct{})
				release := make(chan struct{})

				loader := WorkspaceLoader(func(ctx context.Context, folder uri.URI) (WorkspaceSnapshot, error) {
					close(started)
					<-release

					return WorkspaceSnapshot{Projects: []Project{{
						ConfigURI:   uri.File("/workspace/project.json"),
						RootURI:     workspace,
						TargetFiles: []uri.URI{file},
					}}}, nil
				})

				srv := NewServer(cache.NewMemFS(map[uri.URI][]byte{
					file: []byte("struct DiskVersion {}"),
				}), nil, Options{WorkspaceLoader: loader, ConfigPath: "pinned"})

				_, err := srv.Initialize(t.Context(), testInitializeParams([]protocol.WorkspaceFolder{{URI: workspace}}))
				require.NoError(t, err)
				require.NoError(t, srv.Initialized(t.Context(), &protocol.InitializedParams{}))
				<-started

				require.NoError(t, srv.DidOpen(t.Context(), &protocol.DidOpenTextDocumentParams{
					TextDocument: protocol.TextDocumentItem{
						URI:        file,
						LanguageID: LanguageIDThrift,
						Text:       "struct OpenVersion {}",
					},
				}))
				changeErr := tt.afterOpen(t.Context(), srv, file)

				close(release)
				synctest.Wait()
				require.NoError(t, changeErr)

				view, err := srv.session.ViewOf(file)
				require.NoError(t, err)
				parsed, err := view.Parse(t.Context(), file)
				require.NoError(t, err)
				assert.Contains(t, parsed.Definitions(), tt.wantDef)
				assert.NotContains(t, parsed.Definitions(), "OpenVersion")
				assert.Equal(t, tt.wantOverlay, srv.session.HasOverlay(file))
			})
		})
	}
}

func TestCustomChangesNeverUseAnotherWorkspaceFallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		badFolder := uri.File("/bad")
		badFile := uri.File("/bad/api.thrift")
		goodFolder := uri.File("/good")
		goodFile := uri.File("/good/api.thrift")

		loader := WorkspaceLoader(func(ctx context.Context, folder uri.URI) (WorkspaceSnapshot, error) {
			if folder == badFolder {
				return WorkspaceSnapshot{}, errors.New("bad workspace")
			}

			return WorkspaceSnapshot{Projects: []Project{{
				ConfigURI:   uri.File("/good/project.json"),
				RootURI:     goodFolder,
				TargetFiles: []uri.URI{goodFile},
			}}}, nil
		})

		srv := NewServer(cache.NewMemFS(map[uri.URI][]byte{
			goodFile: []byte("struct Good {}"),
		}), nil, Options{WorkspaceLoader: loader, ConfigPath: "pinned"})

		_, err := srv.Initialize(t.Context(), testInitializeParams([]protocol.WorkspaceFolder{{URI: badFolder}, {URI: goodFolder}}))
		require.NoError(t, err)
		require.NoError(t, srv.Initialized(t.Context(), &protocol.InitializedParams{}))
		synctest.Wait()

		goodView, err := srv.session.ViewOf(goodFile)
		require.NoError(t, err)
		_, err = srv.viewOf(badFile)
		require.Error(t, err, "a file without snapshot ownership must not use another workspace's view")

		openDocument(t, srv, badFile, "struct BadOpen {}")
		require.NoError(t, srv.DidChange(t.Context(), &protocol.DidChangeTextDocumentParams{
			TextDocument: protocol.VersionedTextDocumentIdentifier{
				TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: badFile},
				Version:                1,
			},
			ContentChanges: []protocol.TextDocumentContentChangeEvent{
				&protocol.TextDocumentContentChangeWholeDocument{Text: "struct BadChanged {}"},
			},
		}))
		assert.False(t, goodView.FileKnown(badFile))

		require.NoError(t, srv.DidClose(t.Context(), &protocol.DidCloseTextDocumentParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: badFile},
		}))
		assert.False(t, goodView.FileKnown(badFile))
		assert.False(t, srv.session.HasOverlay(badFile))
	})
}

func TestCustomWatchedNonTargetIsNotIndexed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := uri.File("/workspace/project")
		target := uri.File("/workspace/project/api.thrift")
		nonTarget := uri.File("/workspace/project/stray.thrift")
		client := &diagClient{}
		loader := WorkspaceLoader(func(context.Context, uri.URI) (WorkspaceSnapshot, error) {
			return WorkspaceSnapshot{Projects: []Project{{
				ConfigURI:   uri.File("/workspace/project/tbuild.yaml"),
				RootURI:     root,
				TargetFiles: []uri.URI{target},
			}}}, nil
		})
		srv := NewServer(cache.NewMemFS(map[uri.URI][]byte{
			target:    []byte("struct API {}"),
			nonTarget: []byte("struct Stray { 1: Missing value }"),
		}), client, Options{WorkspaceLoader: loader, ConfigPath: "pinned"})

		_, err := srv.Initialize(t.Context(), testInitializeParams([]protocol.WorkspaceFolder{{URI: root}}))
		require.NoError(t, err)
		require.NoError(t, srv.Initialized(t.Context(), &protocol.InitializedParams{}))
		synctest.Wait()
		client.reset()

		require.NoError(t, srv.DidChangeWatchedFiles(t.Context(), &protocol.DidChangeWatchedFilesParams{
			Changes: []protocol.FileEvent{{URI: nonTarget, Type: protocol.FileChangeTypeChanged}},
		}))
		synctest.Wait()

		view, err := srv.session.ViewOf(target)
		require.NoError(t, err)
		assert.False(t, view.FileKnown(nonTarget))
		assert.Empty(t, client.last(nonTarget), "watched non-target must not publish diagnostics")
		assert.Nil(t, srv.reportFor(nonTarget), "watched non-target must not cache diagnostics")
		result, err := srv.Symbols(t.Context(), &protocol.WorkspaceSymbolParams{Query: ""})
		require.NoError(t, err)
		symbols, ok := result.(protocol.SymbolInformationSlice)
		require.True(t, ok)
		names := symbolNames(symbols)
		assert.Contains(t, names, "API")
		assert.NotContains(t, names, "Stray")
	})
}

func TestCustomWorkspaceSymbolsExcludeIncludedNonTarget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := uri.File("/workspace/project")
		target := uri.File("/workspace/project/api.thrift")
		included := uri.File("/workspace/project/stray.thrift")
		loader := WorkspaceLoader(func(context.Context, uri.URI) (WorkspaceSnapshot, error) {
			return WorkspaceSnapshot{Projects: []Project{{
				ConfigURI:   uri.File("/workspace/project/tbuild.yaml"),
				RootURI:     root,
				TargetFiles: []uri.URI{target},
			}}}, nil
		})
		srv := NewServer(cache.NewMemFS(map[uri.URI][]byte{
			target:   []byte("include \"stray.thrift\"\nstruct API { 1: stray.Stray value }"),
			included: []byte("struct Stray {}"),
		}), nil, Options{WorkspaceLoader: loader, ConfigPath: "pinned"})

		_, err := srv.Initialize(t.Context(), testInitializeParams([]protocol.WorkspaceFolder{{URI: root}}))
		require.NoError(t, err)
		require.NoError(t, srv.Initialized(t.Context(), &protocol.InitializedParams{}))
		synctest.Wait()

		view, err := srv.viewOf(target)
		require.NoError(t, err)
		require.True(t, view.FileKnown(included), "recursive analysis must retain included files in the view cache")

		result, err := srv.Symbols(t.Context(), &protocol.WorkspaceSymbolParams{Query: ""})
		require.NoError(t, err)
		symbols, ok := result.(protocol.SymbolInformationSlice)
		require.True(t, ok)
		names := symbolNames(symbols)
		assert.Contains(t, names, "API")
		assert.NotContains(t, names, "Stray")

		locations, err := srv.definition(t.Context(), &protocol.DefinitionParams{
			TextDocumentPositionParams: protocol.TextDocumentPositionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: target},
				Position:     protocol.Position{Line: 1, Character: 24},
			},
		})
		require.NoError(t, err)
		require.Len(t, locations, 1)
		assert.Equal(t, included, locations[0].URI)
	})
}

func TestCustomWatchedFilesDoNotReadUnownedEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := uri.File("/workspace/project")
		target := uri.File("/workspace/project/api.thrift")
		unowned := uri.File("/workspace/project/stray.thrift")
		fs := &watchSpyFS{
			FileSource: cache.NewMemFS(map[uri.URI][]byte{target: []byte("struct Before {}")}),
			forbidden:  unowned,
		}
		loader := WorkspaceLoader(func(context.Context, uri.URI) (WorkspaceSnapshot, error) {
			return WorkspaceSnapshot{Projects: []Project{{
				ConfigURI:   uri.File("/workspace/project/tbuild.yaml"),
				RootURI:     root,
				TargetFiles: []uri.URI{target},
			}}}, nil
		})
		srv := NewServer(fs, nil, Options{WorkspaceLoader: loader, ConfigPath: "pinned"})

		_, err := srv.Initialize(t.Context(), testInitializeParams([]protocol.WorkspaceFolder{{URI: root}}))
		require.NoError(t, err)
		require.NoError(t, srv.Initialized(t.Context(), &protocol.InitializedParams{}))
		synctest.Wait()
		fs.reads.Store(0)
		fs.contents.Store(0)

		for _, eventType := range []protocol.FileChangeType{
			protocol.FileChangeTypeChanged,
			protocol.FileChangeTypeCreated,
			protocol.FileChangeTypeDeleted,
		} {
			err = srv.DidChangeWatchedFiles(t.Context(), &protocol.DidChangeWatchedFilesParams{Changes: []protocol.FileEvent{
				{URI: unowned, Type: eventType},
			}})
			require.NoError(t, err)
		}
		assert.Zero(t, fs.reads.Load())
		assert.Zero(t, fs.contents.Load())

		err = srv.DidChangeWatchedFiles(t.Context(), &protocol.DidChangeWatchedFilesParams{Changes: []protocol.FileEvent{
			{URI: target, Type: protocol.FileChangeTypeChanged},
		}})
		require.NoError(t, err)
		synctest.Wait()
		assert.Positive(t, fs.reads.Load())
		assert.Positive(t, fs.contents.Load())
	})
}

type watchSpyFS struct {
	cache.FileSource
	forbidden uri.URI
	reads     atomic.Int32
	contents  atomic.Int32
}

func (fs *watchSpyFS) ReadFile(ctx context.Context, file uri.URI) (cache.FileHandle, error) {
	fs.reads.Add(1)
	if file == fs.forbidden {
		return nil, errors.New("unowned file was read")
	}

	handle, err := fs.FileSource.ReadFile(ctx, file)
	if err != nil {
		return nil, err
	}

	return &watchSpyHandle{FileHandle: handle, contents: &fs.contents}, nil
}

type watchSpyHandle struct {
	cache.FileHandle
	contents *atomic.Int32
}

func (h *watchSpyHandle) Content() ([]byte, error) {
	h.contents.Add(1)

	return h.FileHandle.Content()
}

func TestCustomClosingOpenNonTargetEvictsIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := uri.File("/workspace/project")
		target := uri.File("/workspace/project/api.thrift")
		nonTarget := uri.File("/workspace/project/stray.thrift")
		client := &diagClient{}
		loader := WorkspaceLoader(func(context.Context, uri.URI) (WorkspaceSnapshot, error) {
			return WorkspaceSnapshot{Projects: []Project{{
				ConfigURI:   uri.File("/workspace/project/tbuild.yaml"),
				RootURI:     root,
				TargetFiles: []uri.URI{target},
			}}}, nil
		})
		srv := NewServer(cache.NewMemFS(map[uri.URI][]byte{
			target:    []byte("struct API {}"),
			nonTarget: []byte("struct DiskStray { 1: Missing value }"),
		}), client, Options{WorkspaceLoader: loader, ConfigPath: "pinned"})

		_, err := srv.Initialize(t.Context(), testInitializeParams([]protocol.WorkspaceFolder{{URI: root}}))
		require.NoError(t, err)
		require.NoError(t, srv.Initialized(t.Context(), &protocol.InitializedParams{}))
		synctest.Wait()

		openDocument(t, srv, nonTarget, "struct OpenStray { 1: Missing value }")
		synctest.Wait()
		view, err := srv.session.ViewOf(target)
		require.NoError(t, err)
		require.True(t, view.FileKnown(nonTarget))
		require.NotEmpty(t, client.last(nonTarget))
		result, err := srv.Symbols(t.Context(), &protocol.WorkspaceSymbolParams{Query: ""})
		require.NoError(t, err)
		symbols, ok := result.(protocol.SymbolInformationSlice)
		require.True(t, ok)
		assert.Contains(t, symbolNames(symbols), "OpenStray")

		require.NoError(t, srv.DidClose(t.Context(), &protocol.DidCloseTextDocumentParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: nonTarget},
		}))
		synctest.Wait()

		assert.False(t, srv.session.HasOverlay(nonTarget))
		assert.False(t, view.FileKnown(nonTarget))
		assert.Empty(t, client.last(nonTarget), "closing the final non-target owner must clear diagnostics")
		result, err = srv.Symbols(t.Context(), &protocol.WorkspaceSymbolParams{Query: ""})
		require.NoError(t, err)
		symbols, ok = result.(protocol.SymbolInformationSlice)
		require.True(t, ok)
		assert.Equal(t, []string{"API"}, symbolNames(symbols))
	})
}

func TestWorkspaceFolderAdditionIsCanceledOnShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		folder := uri.File("/added")
		started := make(chan struct{})
		canceled := make(chan error, 1)
		release := make(chan struct{})

		loader := WorkspaceLoader(func(ctx context.Context, got uri.URI) (WorkspaceSnapshot, error) {
			close(started)

			select {
			case <-ctx.Done():
				canceled <- ctx.Err()

				return WorkspaceSnapshot{}, ctx.Err()
			case <-release:
				return WorkspaceSnapshot{}, nil
			}
		})

		srv := NewServer(cache.NewMemFS(nil), nil, Options{WorkspaceLoader: loader, ConfigPath: "pinned"})
		_, err := srv.Initialize(t.Context(), &protocol.InitializeParams{})
		require.NoError(t, err)
		require.NoError(t, srv.Initialized(t.Context(), &protocol.InitializedParams{}))

		result := make(chan error, 1)
		go func() {
			result <- srv.DidChangeWorkspaceFolders(t.Context(), &protocol.DidChangeWorkspaceFoldersParams{
				Event: protocol.WorkspaceFoldersChangeEvent{
					Added: []protocol.WorkspaceFolder{{URI: folder}},
				},
			})
		}()
		<-started

		require.NoError(t, srv.Shutdown(t.Context()))
		synctest.Wait()

		select {
		case err := <-canceled:
			require.ErrorIs(t, err, context.Canceled)
			require.NoError(t, <-result, "workspace-folder notifications must not return loader errors")
		default:
			close(release)
			synctest.Wait()
			t.Fatal("workspace-folder loader did not observe shutdown cancellation")
		}
	})
}

func TestRemovedFolderIsNotResurrectedByInFlightLoad(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		folder := uri.File("/workspace")
		projectRoot := uri.File("/workspace/project")
		started := make(chan struct{})
		release := make(chan struct{})

		loader := WorkspaceLoader(func(ctx context.Context, got uri.URI) (WorkspaceSnapshot, error) {
			close(started)
			<-release // Deliberately ignore cancellation to exercise generation invalidation.

			return WorkspaceSnapshot{Projects: []Project{{
				ConfigURI: uri.File("/workspace/project.json"),
				RootURI:   projectRoot,
			}}}, nil
		})

		srv := NewServer(cache.NewMemFS(nil), nil, Options{WorkspaceLoader: loader, ConfigPath: "pinned"})
		_, err := srv.Initialize(t.Context(), &protocol.InitializeParams{})
		require.NoError(t, err)
		require.NoError(t, srv.Initialized(t.Context(), &protocol.InitializedParams{}))

		result := make(chan error, 1)
		go func() {
			result <- srv.DidChangeWorkspaceFolders(t.Context(), &protocol.DidChangeWorkspaceFoldersParams{
				Event: protocol.WorkspaceFoldersChangeEvent{
					Added: []protocol.WorkspaceFolder{{URI: folder}},
				},
			})
		}()
		<-started

		require.NoError(t, srv.DidChangeWorkspaceFolders(t.Context(), &protocol.DidChangeWorkspaceFoldersParams{
			Event: protocol.WorkspaceFoldersChangeEvent{
				Removed: []protocol.WorkspaceFolder{{URI: folder}},
			},
		}))
		close(release)
		synctest.Wait()

		require.NoError(t, <-result)
		assertViewMissing(t, srv, projectRoot)
	})
}

func TestWorkspaceTargetsAreIndexedOncePerView(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		workspace := uri.File("/workspace")
		first := uri.File("/workspace/first.thrift")
		second := uri.File("/workspace/second.thrift")
		client := &diagClient{}

		loader := WorkspaceLoader(func(ctx context.Context, folder uri.URI) (WorkspaceSnapshot, error) {
			return WorkspaceSnapshot{Projects: []Project{{
				ConfigURI:   uri.File("/workspace/project.json"),
				RootURI:     workspace,
				TargetFiles: []uri.URI{first, second},
			}}}, nil
		})

		srv := NewServer(cache.NewMemFS(map[uri.URI][]byte{
			first:  []byte("struct First {"),
			second: []byte("struct Second {"),
		}), client, Options{WorkspaceLoader: loader, ConfigPath: "pinned"})

		_, err := srv.Initialize(t.Context(), testInitializeParams([]protocol.WorkspaceFolder{{URI: workspace}}))
		require.NoError(t, err)
		require.NoError(t, srv.Initialized(t.Context(), &protocol.InitializedParams{}))
		synctest.Wait()

		view, err := srv.session.ViewOf(first)
		require.NoError(t, err)
		assert.Equal(t, uint64(1), view.Generation())
		assert.NotEmpty(t, client.last(first))
		assert.NotEmpty(t, client.last(second))
	})
}

func TestWorkspaceIssuesPersistAcrossFolders(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		folderA := uri.File("/workspace-a")
		folderB := uri.File("/workspace-b")
		issueURI := uri.File("/shared/project.json")
		client := &diagClient{}

		loader := WorkspaceLoader(func(ctx context.Context, folder uri.URI) (WorkspaceSnapshot, error) {
			return WorkspaceSnapshot{Issues: []WorkspaceIssue{{
				URI:     issueURI,
				Message: fmt.Sprintf("issue from %s", folder),
			}}}, nil
		})

		srv := NewServer(cache.NewMemFS(nil), client, Options{WorkspaceLoader: loader, ConfigPath: "pinned"})
		_, err := srv.Initialize(t.Context(), testInitializeParams([]protocol.WorkspaceFolder{{URI: folderA}, {URI: folderB}}))
		require.NoError(t, err)
		require.NoError(t, srv.Initialized(t.Context(), &protocol.InitializedParams{}))
		synctest.Wait()

		assert.ElementsMatch(t, []string{
			"issue from file:///workspace-a",
			"issue from file:///workspace-b",
		}, diagMessages(client.last(issueURI)))

		require.NoError(t, srv.DidChangeWorkspaceFolders(t.Context(), &protocol.DidChangeWorkspaceFoldersParams{
			Event: protocol.WorkspaceFoldersChangeEvent{
				Removed: []protocol.WorkspaceFolder{{URI: folderB}},
			},
		}))
		assert.Equal(t, []string{"issue from file:///workspace-a"}, diagMessages(client.last(issueURI)))

		require.NoError(t, srv.DidChangeWorkspaceFolders(t.Context(), &protocol.DidChangeWorkspaceFoldersParams{
			Event: protocol.WorkspaceFoldersChangeEvent{
				Removed: []protocol.WorkspaceFolder{{URI: folderA}},
			},
		}))
		assert.Empty(t, client.last(issueURI), "removing the final owner must clear stale diagnostics")
	})
}

func TestSharedProjectViewSurvivesWorkspaceFolderRemoval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		folderA := uri.File("/workspace-a")
		folderB := uri.File("/workspace-b")
		projectRoot := uri.File("/shared/project")

		loader := WorkspaceLoader(func(ctx context.Context, folder uri.URI) (WorkspaceSnapshot, error) {
			return WorkspaceSnapshot{Projects: []Project{{
				ConfigURI: uri.URI(fmt.Sprintf("%s/project.json", folder)),
				RootURI:   projectRoot,
			}}}, nil
		})

		srv := NewServer(cache.NewMemFS(nil), nil, Options{WorkspaceLoader: loader, ConfigPath: "pinned"})
		_, err := srv.Initialize(t.Context(), testInitializeParams([]protocol.WorkspaceFolder{{URI: folderA}, {URI: folderB}}))
		require.NoError(t, err)
		require.NoError(t, srv.Initialized(t.Context(), &protocol.InitializedParams{}))
		synctest.Wait()

		assertViewPresent(t, srv, projectRoot)
		require.NoError(t, srv.DidChangeWorkspaceFolders(t.Context(), &protocol.DidChangeWorkspaceFoldersParams{
			Event: protocol.WorkspaceFoldersChangeEvent{
				Removed: []protocol.WorkspaceFolder{{URI: folderA}},
			},
		}))
		assertViewPresent(t, srv, projectRoot)

		require.NoError(t, srv.DidChangeWorkspaceFolders(t.Context(), &protocol.DidChangeWorkspaceFoldersParams{
			Event: protocol.WorkspaceFoldersChangeEvent{
				Removed: []protocol.WorkspaceFolder{{URI: folderB}},
			},
		}))
		assertViewMissing(t, srv, projectRoot)
	})
}

func TestSharedRootEvictsTargetsWhenFolderLosesOwnership(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		folderA := uri.File("/workspace-a")
		folderB := uri.File("/workspace-b")
		root := uri.File("/shared/project")
		targetA := uri.File("/shared/project/a.thrift")
		targetB := uri.File("/shared/project/b.thrift")
		client := &diagClient{}

		loader := WorkspaceLoader(func(ctx context.Context, folder uri.URI) (WorkspaceSnapshot, error) {
			target := targetA
			if folder == folderB {
				target = targetB
			}

			return WorkspaceSnapshot{Projects: []Project{{
				ConfigURI:   uri.File(fmt.Sprintf("%s/project.json", folder)),
				RootURI:     root,
				TargetFiles: []uri.URI{target},
			}}}, nil
		})

		srv := NewServer(cache.NewMemFS(map[uri.URI][]byte{
			targetA: []byte("struct A {}"),
			targetB: []byte("struct B { 1: Missing value }"),
		}), client, Options{WorkspaceLoader: loader, ConfigPath: "pinned"})

		_, err := srv.Initialize(t.Context(), testInitializeParams([]protocol.WorkspaceFolder{{URI: folderA}, {URI: folderB}}))
		require.NoError(t, err)
		require.NoError(t, srv.Initialized(t.Context(), &protocol.InitializedParams{}))
		synctest.Wait()

		view, err := srv.session.ViewOf(targetA)
		require.NoError(t, err)
		assert.True(t, view.FileKnown(targetA))
		assert.True(t, view.FileKnown(targetB))
		assert.NotEmpty(t, client.last(targetB))

		require.NoError(t, srv.DidChangeWorkspaceFolders(t.Context(), &protocol.DidChangeWorkspaceFoldersParams{
			Event: protocol.WorkspaceFoldersChangeEvent{
				Removed: []protocol.WorkspaceFolder{{URI: folderB}},
			},
		}))
		synctest.Wait()

		assert.False(t, view.FileKnown(targetB), "a target removed from the snapshot must leave the retained view")
		assert.Empty(t, client.last(targetB), "removing the final target owner must clear its diagnostics")
		result, err := srv.Symbols(t.Context(), &protocol.WorkspaceSymbolParams{Query: ""})
		require.NoError(t, err)
		symbols, ok := result.(protocol.SymbolInformationSlice)
		require.True(t, ok)
		assert.Equal(t, []string{"A"}, symbolNames(symbols))
	})
}

func TestRemovingFinalCustomViewClearsDiagnostics(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		folder := uri.File("/workspace")
		root := uri.File("/workspace/project")
		target := uri.File("/workspace/project/api.thrift")
		client := &diagClient{}

		loader := WorkspaceLoader(func(ctx context.Context, got uri.URI) (WorkspaceSnapshot, error) {
			return WorkspaceSnapshot{Projects: []Project{{
				ConfigURI:   uri.File("/workspace/project.json"),
				RootURI:     root,
				TargetFiles: []uri.URI{target},
			}}}, nil
		})

		srv := NewServer(cache.NewMemFS(map[uri.URI][]byte{
			target: []byte("struct A { 1: Missing value }"),
		}), client, Options{WorkspaceLoader: loader, ConfigPath: "pinned"})

		_, err := srv.Initialize(t.Context(), testInitializeParams([]protocol.WorkspaceFolder{{URI: folder}}))
		require.NoError(t, err)
		require.NoError(t, srv.Initialized(t.Context(), &protocol.InitializedParams{}))
		synctest.Wait()
		require.NotEmpty(t, client.last(target))

		require.NoError(t, srv.DidChangeWorkspaceFolders(t.Context(), &protocol.DidChangeWorkspaceFoldersParams{
			Event: protocol.WorkspaceFoldersChangeEvent{
				Removed: []protocol.WorkspaceFolder{{URI: folder}},
			},
		}))

		assert.Empty(t, srv.session.Views())
		assert.Empty(t, client.last(target), "removing the final view must publish a clear diagnostic set")
	})
}

func TestCustomOpenOverlayMovesToNewOwner(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		outerFolder := uri.File("/workspace")
		innerFolder := uri.File("/workspace/service")
		file := uri.File("/workspace/service/api.thrift")

		loader := WorkspaceLoader(func(ctx context.Context, folder uri.URI) (WorkspaceSnapshot, error) {
			if folder == innerFolder {
				return WorkspaceSnapshot{Projects: []Project{{
					ConfigURI: uri.File("/workspace/service/project.json"),
					RootURI:   innerFolder,
				}}}, nil
			}

			return WorkspaceSnapshot{Projects: []Project{{
				ConfigURI:   uri.File("/workspace/project.json"),
				RootURI:     outerFolder,
				TargetFiles: []uri.URI{file},
			}}}, nil
		})

		srv := NewServer(cache.NewMemFS(map[uri.URI][]byte{
			file: []byte("struct DiskVersion {}"),
		}), nil, Options{WorkspaceLoader: loader, ConfigPath: "pinned"})

		_, err := srv.Initialize(t.Context(), testInitializeParams([]protocol.WorkspaceFolder{{URI: outerFolder}}))
		require.NoError(t, err)
		require.NoError(t, srv.Initialized(t.Context(), &protocol.InitializedParams{}))
		synctest.Wait()

		openDocument(t, srv, file, "struct OverlayVersion {}")
		outerView, err := srv.session.ViewOf(file)
		require.NoError(t, err)
		assert.Equal(t, outerFolder, outerView.Folder())

		require.NoError(t, srv.DidChangeWorkspaceFolders(t.Context(), &protocol.DidChangeWorkspaceFoldersParams{
			Event: protocol.WorkspaceFoldersChangeEvent{
				Added: []protocol.WorkspaceFolder{{URI: innerFolder}},
			},
		}))
		synctest.Wait()

		innerView, err := srv.session.ViewOf(file)
		require.NoError(t, err)
		assert.Equal(t, innerFolder, innerView.Folder())
		assert.False(t, outerView.FileKnown(file))
		parsed, err := innerView.Parse(t.Context(), file)
		require.NoError(t, err)
		assert.Contains(t, parsed.Definitions(), "OverlayVersion")
		assert.True(t, srv.session.HasOverlay(file))
	})
}
