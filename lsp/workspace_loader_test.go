package lsp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/formatter"
	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/options"
)

func TestWorkspaceLoaderUsesConfigFinderPerProjectRoot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		roots := []string{filepath.Join(dir, "one"), filepath.Join(dir, "two")}
		widths := []int{91, 92}
		configs := make(map[string]string, len(roots))
		projects := make([]Project, len(roots))
		files := make(map[uri.URI][]byte, len(roots))
		for i, root := range roots {
			require.NoError(t, os.MkdirAll(root, 0o755))
			config := filepath.Join(root, options.ConfigFileName)
			require.NoError(t, os.WriteFile(config, fmt.Appendf(nil, `{"printWidth":%d}`, widths[i]), 0o644))
			configs[root] = config
			target := uri.File(filepath.Join(root, "api.thrift"))
			files[target] = []byte("struct API {}")
			projects[i] = Project{
				ConfigURI:   uri.File(filepath.Join(root, "tbuild.yaml")),
				RootURI:     uri.File(root),
				TargetFiles: []uri.URI{target},
			}
		}

		var calls []string
		finder := func(root string) (string, error) {
			calls = append(calls, root)

			return configs[root], nil
		}
		loader := func(context.Context, uri.URI) (WorkspaceSnapshot, error) {
			return WorkspaceSnapshot{Projects: projects}, nil
		}
		srv := NewServer(cache.NewMemFS(files), nil, Options{
			ConfigFinder:    finder,
			WorkspaceLoader: loader,
		})

		_, err := srv.Initialize(t.Context(), testInitializeParams([]protocol.WorkspaceFolder{{URI: uri.File(dir)}}))
		require.NoError(t, err)
		require.NoError(t, srv.Initialized(t.Context(), &protocol.InitializedParams{}))
		synctest.Wait()

		slices.Sort(calls)
		assert.Equal(t, roots, calls)
		for i, root := range roots {
			cfg := srv.folderConfig(uri.File(root))
			require.NotNil(t, cfg.PrintWidth)
			assert.Equal(t, widths[i], *cfg.PrintWidth)
		}
	})
}

func TestExplicitConfigPathBypassesConfigFinder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := uri.File("/workspace/project")
		target := uri.File("/workspace/project/api.thrift")
		width := 97
		var calls atomic.Int32
		finder := func(string) (string, error) {
			calls.Add(1)

			return "", nil
		}
		loader := func(context.Context, uri.URI) (WorkspaceSnapshot, error) {
			return WorkspaceSnapshot{Projects: []Project{{
				ConfigURI:   uri.File("/workspace/project/tbuild.yaml"),
				RootURI:     root,
				TargetFiles: []uri.URI{target},
			}}}, nil
		}
		srv := NewServer(cache.NewMemFS(map[uri.URI][]byte{target: []byte("struct API {}")}), nil, Options{
			Config:          options.Patch{FormatPatch: formatter.FormatPatch{PrintWidth: &width}},
			ConfigPath:      "/pinned/thrift-ls.json",
			ConfigFinder:    finder,
			WorkspaceLoader: loader,
		})

		_, err := srv.Initialize(t.Context(), testInitializeParams([]protocol.WorkspaceFolder{{URI: uri.File("/workspace")}}))
		require.NoError(t, err)
		require.NoError(t, srv.Initialized(t.Context(), &protocol.InitializedParams{}))
		synctest.Wait()

		assert.Zero(t, calls.Load())
		cfg := srv.folderConfig(root)
		require.NotNil(t, cfg.PrintWidth)
		assert.Equal(t, width, *cfg.PrintWidth)
	})
}

func TestWorkspaceLoaderDefersViewsAndIndexesOverlayInMostSpecificProject(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		workspace := uri.File("/workspace")
		outerFile := uri.File("/workspace/root.thrift")
		innerRoot := uri.File("/workspace/service")
		innerFile := uri.File("/workspace/service/api.thrift")
		externalFile := uri.File("/dependencies/shared.thrift")

		started := make(chan struct{})
		release := make(chan struct{})
		var calls atomic.Int32

		loader := WorkspaceLoader(func(ctx context.Context, folder uri.URI) (WorkspaceSnapshot, error) {
			calls.Add(1)
			close(started)
			<-release

			return WorkspaceSnapshot{
				Projects: []Project{
					{
						ConfigURI:    uri.File("/workspace/project.json"),
						RootURI:      workspace,
						TargetFiles:  []uri.URI{outerFile, innerFile},
						IncludePaths: []string{"/outer/includes"},
					},
					{
						ConfigURI:    uri.File("/workspace/service/project.json"),
						RootURI:      innerRoot,
						TargetFiles:  []uri.URI{externalFile},
						IncludePaths: []string{"/inner/includes"},
					},
				},
			}, nil
		})

		srv := NewServer(cache.NewMemFS(map[uri.URI][]byte{
			outerFile:    []byte("struct Root {}"),
			innerFile:    []byte("struct DiskVersion {}"),
			externalFile: []byte("struct Shared {}"),
		}), nil, Options{WorkspaceLoader: loader, ConfigPath: "pinned"})

		_, err := srv.Initialize(t.Context(), testInitializeParams([]protocol.WorkspaceFolder{{URI: workspace}}))
		require.NoError(t, err)
		assert.Zero(t, calls.Load(), "loader must not run during initialize")
		assert.Empty(t, srv.session.Views())

		require.NoError(t, srv.Initialized(t.Context(), &protocol.InitializedParams{}))
		<-started

		require.NoError(t, srv.DidOpen(t.Context(), &protocol.DidOpenTextDocumentParams{
			TextDocument: protocol.TextDocumentItem{
				URI:        innerFile,
				LanguageID: LanguageIDThrift,
				Version:    1,
				Text:       "struct OverlayVersion {}",
			},
		}))
		assert.Empty(t, srv.session.Views(), "didOpen must not create a fallback view while loading")

		close(release)
		synctest.Wait()

		require.Equal(t, int32(1), calls.Load())
		require.Len(t, srv.session.Views(), 2)

		outerView, err := srv.session.ViewOf(outerFile)
		require.NoError(t, err)
		assert.Equal(t, workspace, outerView.Folder())
		assert.True(t, outerView.FileKnown(outerFile))

		innerView, err := srv.session.ViewOf(innerFile)
		require.NoError(t, err)
		assert.Equal(t, innerRoot, innerView.Folder())
		assert.True(t, innerView.FileKnown(innerFile), "all views must exist before targets are routed")
		assert.Equal(t, []string{"/inner/includes"}, innerView.Resolver().IncludePaths())

		parsed, err := innerView.Parse(t.Context(), innerFile)
		require.NoError(t, err)
		assert.Contains(t, parsed.Definitions(), "OverlayVersion")
		assert.NotContains(t, parsed.Definitions(), "DiskVersion")

		externalView, err := srv.session.ViewOf(externalFile)
		require.NoError(t, err)
		assert.Equal(t, innerRoot, externalView.Folder(), "an external target stays in its declared project")

		result, err := srv.Symbols(t.Context(), &protocol.WorkspaceSymbolParams{Query: ""})
		require.NoError(t, err)
		symbols, ok := result.(protocol.SymbolInformationSlice)
		require.True(t, ok)
		assert.Contains(t, symbolNames(symbols), "Shared", "an external target remains a workspace symbol")
	})
}

func TestWorkspaceLoaderIsCanceledOnShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		workspace := uri.File("/workspace")
		started := make(chan struct{})
		canceled := make(chan error, 1)
		release := make(chan struct{})

		loader := WorkspaceLoader(func(ctx context.Context, folder uri.URI) (WorkspaceSnapshot, error) {
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

		_, err := srv.Initialize(t.Context(), testInitializeParams([]protocol.WorkspaceFolder{{URI: workspace}}))
		require.NoError(t, err)

		requestCtx, cancelRequest := context.WithCancel(t.Context())
		require.NoError(t, srv.Initialized(requestCtx, &protocol.InitializedParams{}))
		<-started

		cancelRequest()
		synctest.Wait()

		select {
		case err := <-canceled:
			close(release)
			t.Fatalf("loader was canceled with Initialized request: %v", err)
		default:
		}

		require.NoError(t, srv.Shutdown(t.Context()))
		synctest.Wait()

		select {
		case err := <-canceled:
			require.ErrorIs(t, err, context.Canceled)
		default:
			close(release)
			synctest.Wait()
			t.Fatal("loader did not observe cancellation on shutdown")
		}
	})
}

func TestWorkspaceLoaderFailureDoesNotCreateFallbackView(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		workspace := uri.File("/workspace")
		file := uri.File("/workspace/api.thrift")
		started := make(chan struct{})
		release := make(chan struct{})

		loader := WorkspaceLoader(func(ctx context.Context, folder uri.URI) (WorkspaceSnapshot, error) {
			close(started)
			<-release

			return WorkspaceSnapshot{}, errors.New("discovery failed")
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
				Text:       "struct OverlayVersion {}",
			},
		}))
		close(release)
		synctest.Wait()

		assert.Empty(t, srv.session.Views(), "a deferred open must not create a stock view after loader failure")
		assert.True(t, srv.session.HasOverlay(file), "the editor overlay remains available for a later snapshot")

		require.NoError(t, srv.DidOpen(t.Context(), &protocol.DidOpenTextDocumentParams{
			TextDocument: protocol.TextDocumentItem{
				URI:        file,
				LanguageID: LanguageIDThrift,
				Version:    1,
				Text:       "struct LaterOverlayVersion {}",
			},
		}))
		assert.Empty(t, srv.session.Views(), "later opens remain owned by the custom loader")
	})
}

func TestWorkspaceLoaderPublishesIssuesWithoutDroppingProjects(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		workspace := uri.File("/workspace")
		target := uri.File("/workspace/api.thrift")
		issueURI := uri.File("/workspace/project.json")
		client := &diagClient{}

		loader := WorkspaceLoader(func(ctx context.Context, folder uri.URI) (WorkspaceSnapshot, error) {
			return WorkspaceSnapshot{
				Projects: []Project{{
					ConfigURI:   issueURI,
					RootURI:     workspace,
					TargetFiles: []uri.URI{target},
				}},
				Issues: []WorkspaceIssue{{
					URI:     issueURI,
					Message: "dependency project could not be loaded",
				}, {
					URI:     issueURI,
					Message: "target could not be resolved",
				}},
			}, nil
		})

		srv := NewServer(cache.NewMemFS(map[uri.URI][]byte{
			target: []byte("struct API {}"),
		}), client, Options{WorkspaceLoader: loader, ConfigPath: "pinned"})

		_, err := srv.Initialize(t.Context(), testInitializeParams([]protocol.WorkspaceFolder{{URI: workspace}}))
		require.NoError(t, err)
		require.NoError(t, srv.Initialized(t.Context(), &protocol.InitializedParams{}))
		synctest.Wait()

		view, err := srv.session.ViewOf(target)
		require.NoError(t, err)
		assert.True(t, view.FileKnown(target))

		diagnostics := client.last(issueURI)
		require.Len(t, diagnostics, 2)
		assert.Equal(t, protocol.DiagnosticSeverityError, diagnostics[0].Severity)
		assert.Equal(t, protocol.String("dependency project could not be loaded"), diagnostics[0].Message)
		assert.Equal(t, protocol.String("target could not be resolved"), diagnostics[1].Message)
	})
}

func TestWorkspaceLoaderIssuesOnlyDoesNotCreateFallbackView(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		workspace := uri.File("/workspace")
		file := uri.File("/workspace/api.thrift")
		issueURI := uri.File("/workspace/project.json")

		loader := WorkspaceLoader(func(ctx context.Context, folder uri.URI) (WorkspaceSnapshot, error) {
			return WorkspaceSnapshot{Issues: []WorkspaceIssue{{
				URI:     issueURI,
				Message: "project is invalid",
			}}}, nil
		})

		srv := NewServer(cache.NewMemFS(nil), nil, Options{WorkspaceLoader: loader, ConfigPath: "pinned"})

		_, err := srv.Initialize(t.Context(), testInitializeParams([]protocol.WorkspaceFolder{{URI: workspace}}))
		require.NoError(t, err)
		require.NoError(t, srv.Initialized(t.Context(), &protocol.InitializedParams{}))
		synctest.Wait()

		require.NoError(t, srv.DidOpen(t.Context(), &protocol.DidOpenTextDocumentParams{
			TextDocument: protocol.TextDocumentItem{
				URI:        file,
				LanguageID: LanguageIDThrift,
				Text:       "struct API {}",
			},
		}))
		assert.Empty(t, srv.session.Views())
		assert.True(t, srv.session.HasOverlay(file))
	})
}

func TestWorkspaceLoaderHandlesWorkspaceFolderChanges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		folderA := uri.File("/workspace-a")
		folderB := uri.File("/workspace-b")
		projectRoot := func(folder uri.URI) uri.URI {
			return uri.File(filepath.Join(folder.FsPath(), "service"))
		}
		target := func(folder uri.URI) uri.URI {
			return uri.File(filepath.Join(projectRoot(folder).FsPath(), "api.thrift"))
		}

		loader := WorkspaceLoader(func(ctx context.Context, folder uri.URI) (WorkspaceSnapshot, error) {
			if folder != folderA && folder != folderB {
				return WorkspaceSnapshot{}, errors.New("unknown workspace folder")
			}

			return WorkspaceSnapshot{Projects: []Project{{
				ConfigURI:   uri.File(filepath.Join(folder.FsPath(), "project.json")),
				RootURI:     projectRoot(folder),
				TargetFiles: []uri.URI{target(folder)},
			}}}, nil
		})

		srv := NewServer(cache.NewMemFS(map[uri.URI][]byte{
			target(folderA): []byte("struct A {}"),
			target(folderB): []byte("struct B {}"),
		}), nil, Options{WorkspaceLoader: loader, ConfigPath: "pinned"})

		_, err := srv.Initialize(t.Context(), testInitializeParams([]protocol.WorkspaceFolder{{URI: folderA}}))
		require.NoError(t, err)
		require.NoError(t, srv.Initialized(t.Context(), &protocol.InitializedParams{}))
		synctest.Wait()

		require.NoError(t, srv.DidChangeWorkspaceFolders(t.Context(), &protocol.DidChangeWorkspaceFoldersParams{
			Event: protocol.WorkspaceFoldersChangeEvent{
				Added: []protocol.WorkspaceFolder{{URI: folderB}},
			},
		}))
		synctest.Wait()
		assertViewPresent(t, srv, projectRoot(folderA))
		assertViewPresent(t, srv, projectRoot(folderB))

		require.NoError(t, srv.DidChangeWorkspaceFolders(t.Context(), &protocol.DidChangeWorkspaceFoldersParams{
			Event: protocol.WorkspaceFoldersChangeEvent{
				Removed: []protocol.WorkspaceFolder{{URI: folderB}},
			},
		}))
		assertViewPresent(t, srv, projectRoot(folderA))
		assertViewMissing(t, srv, projectRoot(folderB))

		err = srv.DidChangeWorkspaceFolders(t.Context(), &protocol.DidChangeWorkspaceFoldersParams{
			Event: protocol.WorkspaceFoldersChangeEvent{
				Added: []protocol.WorkspaceFolder{{URI: uri.File("/unknown")}},
			},
		})
		require.NoError(t, err)
		synctest.Wait()
		assertViewPresent(t, srv, projectRoot(folderA))
	})
}

func TestWorkspaceLoaderRejectsInvalidProjectsAndConflictingRoots(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		workspace := uri.File("/workspace")
		validRoot := uri.File("/workspace/service")
		validTarget := uri.File("/workspace/service/api.thrift")
		invalidRootTarget := uri.File("/invalid-root/api.thrift")
		invalidConfigTarget := uri.File("/invalid-config/api.thrift")
		conflictingTarget := uri.File("/workspace/service/conflicting.thrift")
		client := &diagClient{}

		loader := WorkspaceLoader(func(ctx context.Context, folder uri.URI) (WorkspaceSnapshot, error) {
			return WorkspaceSnapshot{Projects: []Project{
				{
					ConfigURI:   uri.File("/workspace/invalid-root.json"),
					RootURI:     uri.URI("not a URI"),
					TargetFiles: []uri.URI{invalidRootTarget},
				},
				{
					RootURI:     uri.File("/workspace/invalid-config"),
					TargetFiles: []uri.URI{invalidConfigTarget},
				},
				{
					ConfigURI:    uri.File("/workspace/service.json"),
					RootURI:      validRoot,
					TargetFiles:  []uri.URI{validTarget},
					IncludePaths: []string{"/first/includes"},
				},
				{
					ConfigURI:    uri.File("/workspace/conflicting.json"),
					RootURI:      validRoot,
					TargetFiles:  []uri.URI{conflictingTarget},
					IncludePaths: []string{"/second/includes"},
				},
			}}, nil
		})

		srv := NewServer(cache.NewMemFS(map[uri.URI][]byte{
			validTarget:         []byte("struct Valid {}"),
			invalidRootTarget:   []byte("struct InvalidRoot {}"),
			invalidConfigTarget: []byte("struct InvalidConfig {}"),
			conflictingTarget:   []byte("struct Conflicting {}"),
		}), client, Options{WorkspaceLoader: loader, ConfigPath: "pinned"})

		_, err := srv.Initialize(t.Context(), testInitializeParams([]protocol.WorkspaceFolder{{URI: workspace}}))
		require.NoError(t, err)
		require.NoError(t, srv.Initialized(t.Context(), &protocol.InitializedParams{}))
		synctest.Wait()

		views := srv.session.Views()
		require.Len(t, views, 1)
		assert.Equal(t, validRoot, views[0].Folder())
		assert.Equal(t, []string{"/first/includes"}, views[0].Resolver().IncludePaths())
		assert.True(t, views[0].FileKnown(validTarget))
		assert.False(t, views[0].FileKnown(invalidRootTarget))
		assert.False(t, views[0].FileKnown(invalidConfigTarget))
		assert.False(t, views[0].FileKnown(conflictingTarget))

		assert.NotEmpty(t, client.last(uri.File("/workspace/invalid-root.json")))
		assert.NotEmpty(t, client.last(workspace), "an invalid project without a config URI is still reported")
		assert.NotEmpty(t, client.last(uri.File("/workspace/conflicting.json")))
	})
}

func assertViewPresent(t *testing.T, srv *Server, folder uri.URI) {
	t.Helper()

	for _, view := range srv.session.Views() {
		if view.Folder() == folder {
			return
		}
	}

	t.Errorf("view %s is missing", folder)
}

func assertViewMissing(t *testing.T, srv *Server, folder uri.URI) {
	t.Helper()

	for _, view := range srv.session.Views() {
		if view.Folder() == folder {
			t.Errorf("view %s is still present", folder)
		}
	}
}
