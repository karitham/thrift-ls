package lsp

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/options"
	"github.com/karitham/thrift-ls/resolver/resolvertest"
	"github.com/karitham/thrift-ls/store"
)

// Workspace lifecycle: initialize defers the load, folders add/remove.

func TestWorkspaceLifecycle(t *testing.T) {
	t.Run("initialize defers the load until the sync load runs", func(t *testing.T) {
		files := resolvertest.Map{
			"/ws/a.thrift":        []byte("struct FromA {}"),
			"/ws/nested/b.thrift": []byte("struct FromB {}"),
		}.URIs()

		srv := newSyncServerWithOptions(nil, files, Options{})

		_, err := srv.Initialize(t.Context(), testInitializeParams([]protocol.WorkspaceFolder{{URI: uri.File("/ws")}}))
		require.NoError(t, err)
		require.Empty(t, srv.session.Views(), "nothing runs during the handshake")

		srv.workspace.loadSync(t.Context(), []uri.URI{uri.File("/ws")})

		views := srv.session.Views()
		require.Len(t, views, 1)
		assert.Equal(t, uri.File("/ws"), views[0].Folder())

		known := views[0].KnownFiles()
		assert.Contains(t, known, uri.File("/ws/a.thrift"))
		assert.Contains(t, known, uri.File("/ws/nested/b.thrift"))
		assert.Equal(t, []string{"FromA", "FromB"}, workspaceSymbolNames(t, srv))
	})

	t.Run("adding and removing folders loads and drops views", func(t *testing.T) {
		ctx := t.Context()
		files := resolvertest.Map{
			"/ws-a/a.thrift": []byte("struct FromA {}"),
			"/ws-b/b.thrift": []byte("struct FromB {}"),
		}.URIs()

		srv := newSyncServerWithOptions(nil, files, Options{})

		require.NoError(t, srv.DidChangeWorkspaceFolders(ctx, &protocol.DidChangeWorkspaceFoldersParams{
			Event: protocol.WorkspaceFoldersChangeEvent{Added: []protocol.WorkspaceFolder{{URI: uri.File("/ws-a")}}},
		}))
		srv.workspace.loadSync(ctx, []uri.URI{uri.File("/ws-a")})
		require.NoError(t, srv.DidChangeWorkspaceFolders(ctx, &protocol.DidChangeWorkspaceFoldersParams{
			Event: protocol.WorkspaceFoldersChangeEvent{Added: []protocol.WorkspaceFolder{{URI: uri.File("/ws-b")}}},
		}))
		srv.workspace.loadSync(ctx, []uri.URI{uri.File("/ws-b")})
		assert.Len(t, srv.session.Views(), 2)
		assert.Equal(t, []string{"FromA", "FromB"}, workspaceSymbolNames(t, srv))

		require.NoError(t, srv.DidChangeWorkspaceFolders(ctx, &protocol.DidChangeWorkspaceFoldersParams{
			Event: protocol.WorkspaceFoldersChangeEvent{Removed: []protocol.WorkspaceFolder{{URI: uri.File("/ws-a")}}},
		}))
		assert.Len(t, srv.session.Views(), 1)
		assert.Equal(t, []string{"FromB"}, workspaceSymbolNames(t, srv))
	})
}

// TestNestedWorkspaceFolderRouting folds the two nested-folder cases into
// one table: whether the inner folder exists at initialize or is added
// later, its files belong to the inner view exactly once.
func TestNestedWorkspaceFolderRouting(t *testing.T) {
	for _, tt := range []struct {
		name string
		// addLater initializes with only the outer folder, then adds inner.
		addLater bool
	}{
		{name: "both folders at initialize"},
		{name: "inner folder added later", addLater: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			outer := uri.File("/workspace")
			inner := uri.File("/workspace/service")
			rootFile := uri.File("/workspace/root.thrift")
			nested := uri.File("/workspace/service/api.thrift")

			srv := newSyncServerWithOptions(nil,
				resolvertest.Map{
					"/workspace/root.thrift":        []byte("struct Root {}"),
					"/workspace/service/api.thrift": []byte("struct Nested {}"),
				}.URIs(),
				Options{})

			folders := []uri.URI{outer}
			if !tt.addLater {
				folders = append(folders, inner)
			}
			_, err := srv.Initialize(t.Context(), testInitializeParams(foldersFromURIs(folders)))
			require.NoError(t, err)
			srv.workspace.loadSync(t.Context(), []uri.URI{outer})
			if !tt.addLater {
				srv.workspace.loadSync(t.Context(), []uri.URI{inner})
			}

			if tt.addLater {
				outerView, err := srv.session.ViewOf(nested)
				require.NoError(t, err)
				assert.Equal(t, outer, outerView.Folder())
				assert.True(t, outerView.FileKnown(nested))

				require.NoError(t, srv.DidChangeWorkspaceFolders(t.Context(), &protocol.DidChangeWorkspaceFoldersParams{
					Event: protocol.WorkspaceFoldersChangeEvent{Added: []protocol.WorkspaceFolder{{URI: inner}}},
				}))
				srv.workspace.loadSync(t.Context(), []uri.URI{inner})
			}

			outerView, err := srv.session.ViewOf(rootFile)
			require.NoError(t, err)
			assert.Equal(t, outer, outerView.Folder())
			assert.True(t, outerView.FileKnown(rootFile))
			assert.False(t, outerView.FileKnown(nested), "the outer view must not retain a nested project's file")

			innerView, err := srv.session.ViewOf(nested)
			require.NoError(t, err)
			assert.Equal(t, inner, innerView.Folder())
			assert.True(t, innerView.FileKnown(nested))
			if tt.addLater {
				assert.False(t, outerView.FileKnown(nested), "adding a specific folder must evict the old owner")
			}
			assert.Equal(t, []string{"Root", "Nested"}, workspaceSymbolNames(t, srv))
		})
	}
}

// TestDidOpenNewFileInsideLoadedRoot pins that opening an unsaved file
// inside a loaded root joins the root: no spurious inner project splits
// off for a file the loader has never seen.
func TestDidOpenNewFileInsideLoadedRoot(t *testing.T) {
	outer := uri.File("/ws")
	nested := uri.File("/ws/sub/new.thrift")

	srv := newSyncServerWithOptions(nil,
		resolvertest.Map{"/ws/a.thrift": []byte("struct A {}")}.URIs(),
		Options{})
	initWorkspace(t, srv, []uri.URI{outer}, nil)
	require.Len(t, srv.session.Views(), 1)

	openDocument(t, srv, nested, "struct New {}")

	assert.Len(t, srv.session.Views(), 1, "a new file under a known root must not create a folder")
	view, err := srv.viewOf(nested)
	require.NoError(t, err)
	assert.Equal(t, outer, view.Folder())
	assert.True(t, view.FileKnown(nested))
}

// Custom loader: deferred views, overlays, failures, issues.

func TestWorkspaceLoaderDefersViewsAndIndexesOverlayInMostSpecificProject(t *testing.T) {
	workspace := uri.File("/workspace")
	outerFile := uri.File("/workspace/root.thrift")
	innerRoot := uri.File("/workspace/service")
	innerFile := uri.File("/workspace/service/api.thrift")
	externalFile := uri.File("/dependencies/shared.thrift")

	srv := newSyncServerWithOptions(nil,
		resolvertest.Map{
			"/workspace/root.thrift":        []byte("struct Root {}"),
			"/workspace/service/api.thrift": []byte("struct DiskVersion {}"),
			"/dependencies/shared.thrift":   []byte("struct Shared {}"),
		}.URIs(),
		Options{ConfigSource: options.PinnedSource(nil)})
	initCustomFolders(t, srv, []uri.URI{workspace})
	assert.Empty(t, srv.session.Views(), "no views before the snapshot")

	require.NoError(t, srv.DidOpen(t.Context(), &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        innerFile,
			LanguageID: LanguageIDThrift,
			Version:    1,
			Text:       "struct OverlayVersion {}",
		},
	}))
	assert.Empty(t, srv.session.Views(), "didOpen must not create a fallback view while loading")

	installSnapshot(t, srv, workspace, WorkspaceSnapshot{
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
	})

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

	assert.Contains(t, workspaceSymbolNames(t, srv), "Shared", "an external target remains a workspace symbol")
}

func TestWorkspaceLoaderFailureDoesNotCreateFallbackView(t *testing.T) {
	workspace := uri.File("/workspace")
	file := uri.File("/workspace/api.thrift")

	srv := newSyncServerWithOptions(nil,
		resolvertest.Map{"/workspace/api.thrift": []byte("struct DiskVersion {}")}.URIs(),
		Options{ConfigSource: options.PinnedSource(nil)})
	initCustomFolders(t, srv, []uri.URI{workspace})
	installSnapshot(t, srv, workspace, WorkspaceSnapshot{})

	require.NoError(t, srv.DidOpen(t.Context(), &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        file,
			LanguageID: LanguageIDThrift,
			Text:       "struct OverlayVersion {}",
		},
	}))

	assert.Empty(t, srv.session.Views(), "a deferred open must not create a fallback view after loader failure")
	assert.True(t, srv.session.HasOverlay(file), "the editor overlay remains available for a later snapshot")

	require.NoError(t, srv.DidOpen(t.Context(), &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        file,
			LanguageID: LanguageIDThrift,
			Version:    1,
			Text:       "struct LaterOverlayVersion {}",
		},
	}))
	assert.Empty(t, srv.session.Views(), "later opens remain owned by the loader")
}

func TestWorkspaceLoaderPublishesIssuesWithoutDroppingProjects(t *testing.T) {
	workspace := uri.File("/workspace")
	target := uri.File("/workspace/api.thrift")
	issueURI := uri.File("/workspace/project.json")
	client := &testClient{}

	srv := newSyncServerWithOptions(client,
		resolvertest.Map{"/workspace/api.thrift": []byte("struct API {}")}.URIs(),
		Options{ConfigSource: options.PinnedSource(nil)})
	initCustomFolders(t, srv, []uri.URI{workspace})
	installSnapshot(t, srv, workspace, WorkspaceSnapshot{
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
	})

	view, err := srv.session.ViewOf(target)
	require.NoError(t, err)
	assert.True(t, view.FileKnown(target))

	diagnostics := client.last(issueURI)
	require.Len(t, diagnostics, 2)
	assert.Equal(t, protocol.DiagnosticSeverityError, diagnostics[0].Severity)
	assert.Equal(t, protocol.String("dependency project could not be loaded"), diagnostics[0].Message)
	assert.Equal(t, protocol.String("target could not be resolved"), diagnostics[1].Message)
}

func TestWorkspaceLoaderIssuesOnlyDoesNotCreateFallbackView(t *testing.T) {
	workspace := uri.File("/workspace")
	file := uri.File("/workspace/api.thrift")
	issueURI := uri.File("/workspace/project.json")

	srv := newSyncServerWithOptions(nil, nil, Options{ConfigSource: options.PinnedSource(nil)})
	initCustomFolders(t, srv, []uri.URI{workspace})
	installSnapshot(t, srv, workspace, WorkspaceSnapshot{Issues: []WorkspaceIssue{{
		URI:     issueURI,
		Message: "project is invalid",
	}}})

	require.NoError(t, srv.DidOpen(t.Context(), &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        file,
			LanguageID: LanguageIDThrift,
			Text:       "struct API {}",
		},
	}))
	assert.Empty(t, srv.session.Views())
	assert.True(t, srv.session.HasOverlay(file))
}

func TestWorkspaceLoaderHandlesWorkspaceFolderChanges(t *testing.T) {
	folderA := uri.File("/workspace-a")
	folderB := uri.File("/workspace-b")
	projectRoot := func(folder uri.URI) uri.URI {
		return uri.File(string(folder) + "/service")
	}
	target := func(folder uri.URI) uri.URI {
		return uri.File(string(projectRoot(folder)) + "/api.thrift")
	}

	srv := newSyncServerWithOptions(nil,
		resolvertest.Map{
			"/workspace-a/service/api.thrift": []byte("struct A {}"),
			"/workspace-b/service/api.thrift": []byte("struct B {}"),
		}.URIs(),
		Options{ConfigSource: options.PinnedSource(nil)})
	initCustomFolders(t, srv, []uri.URI{folderA})
	installSnapshot(t, srv, folderA, WorkspaceSnapshot{Projects: []Project{{
		ConfigURI:   uri.File(string(folderA) + "/project.json"),
		RootURI:     projectRoot(folderA),
		TargetFiles: []uri.URI{target(folderA)},
	}}})

	require.NoError(t, srv.DidChangeWorkspaceFolders(t.Context(), &protocol.DidChangeWorkspaceFoldersParams{
		Event: protocol.WorkspaceFoldersChangeEvent{
			Added: []protocol.WorkspaceFolder{{URI: folderB}},
		},
	}))
	installSnapshot(t, srv, folderB, WorkspaceSnapshot{Projects: []Project{{
		ConfigURI:   uri.File(string(folderB) + "/project.json"),
		RootURI:     projectRoot(folderB),
		TargetFiles: []uri.URI{target(folderB)},
	}}})
	assertViewPresent(t, srv, projectRoot(folderA))
	assertViewPresent(t, srv, projectRoot(folderB))

	require.NoError(t, srv.DidChangeWorkspaceFolders(t.Context(), &protocol.DidChangeWorkspaceFoldersParams{
		Event: protocol.WorkspaceFoldersChangeEvent{
			Removed: []protocol.WorkspaceFolder{{URI: folderB}},
		},
	}))
	assertViewPresent(t, srv, projectRoot(folderA))
	assertViewMissing(t, srv, projectRoot(folderB))

	err := srv.DidChangeWorkspaceFolders(t.Context(), &protocol.DidChangeWorkspaceFoldersParams{
		Event: protocol.WorkspaceFoldersChangeEvent{
			Added: []protocol.WorkspaceFolder{{URI: uri.File("/unknown")}},
		},
	})
	require.NoError(t, err)
	installSnapshot(t, srv, uri.File("/unknown"), WorkspaceSnapshot{})
	assertViewPresent(t, srv, projectRoot(folderA))
}

func TestWorkspaceLoaderRejectsInvalidProjectsAndConflictingRoots(t *testing.T) {
	workspace := uri.File("/workspace")
	validRoot := uri.File("/workspace/service")
	validTarget := uri.File("/workspace/service/api.thrift")
	invalidRootTarget := uri.File("/invalid-root/api.thrift")
	invalidConfigTarget := uri.File("/invalid-config/api.thrift")
	conflictingTarget := uri.File("/workspace/service/conflicting.thrift")
	client := &testClient{}

	srv := newSyncServerWithOptions(client,
		resolvertest.Map{
			"/workspace/service/api.thrift":         []byte("struct Valid {}"),
			"/invalid-root/api.thrift":              []byte("struct InvalidRoot {}"),
			"/invalid-config/api.thrift":            []byte("struct InvalidConfig {}"),
			"/workspace/service/conflicting.thrift": []byte("struct Conflicting {}"),
		}.URIs(),
		Options{ConfigSource: options.PinnedSource(nil)})
	initCustomFolders(t, srv, []uri.URI{workspace})
	installSnapshot(t, srv, workspace, WorkspaceSnapshot{Projects: []Project{
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
	}})

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
}

// Custom workspace document and watched-file behavior.

func TestCustomDocumentChangesDuringWorkspaceLoad(t *testing.T) {
	for _, tt := range []struct {
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
	} {
		t.Run(tt.name, func(t *testing.T) {
			workspace := uri.File("/workspace")
			file := uri.File("/workspace/api.thrift")

			srv := newSyncServerWithOptions(nil,
				resolvertest.Map{"/workspace/api.thrift": []byte("struct DiskVersion {}")}.URIs(),
				Options{ConfigSource: options.PinnedSource(nil)})
			initCustomFolders(t, srv, []uri.URI{workspace})

			require.NoError(t, srv.DidOpen(t.Context(), &protocol.DidOpenTextDocumentParams{
				TextDocument: protocol.TextDocumentItem{
					URI:        file,
					LanguageID: LanguageIDThrift,
					Text:       "struct OpenVersion {}",
				},
			}))
			require.NoError(t, tt.afterOpen(t.Context(), srv, file))

			installSnapshot(t, srv, workspace, WorkspaceSnapshot{Projects: []Project{{
				ConfigURI:   uri.File("/workspace/project.json"),
				RootURI:     workspace,
				TargetFiles: []uri.URI{file},
			}}})

			view, err := srv.session.ViewOf(file)
			require.NoError(t, err)
			parsed, err := view.Parse(t.Context(), file)
			require.NoError(t, err)
			assert.Contains(t, parsed.Definitions(), tt.wantDef)
			assert.NotContains(t, parsed.Definitions(), "OpenVersion")
			assert.Equal(t, tt.wantOverlay, srv.session.HasOverlay(file))
		})
	}
}

func TestCustomChangesNeverUseAnotherWorkspaceFallback(t *testing.T) {
	badFolder := uri.File("/bad")
	badFile := uri.File("/bad/api.thrift")
	goodFolder := uri.File("/good")
	goodFile := uri.File("/good/api.thrift")

	srv := newSyncServerWithOptions(nil,
		resolvertest.Map{"/good/api.thrift": []byte("struct Good {}")}.URIs(),
		Options{ConfigSource: options.PinnedSource(nil)})
	initCustomFolders(t, srv, []uri.URI{badFolder, goodFolder})
	installSnapshots(t, srv, map[uri.URI]WorkspaceSnapshot{
		badFolder: {},
		goodFolder: {Projects: []Project{{
			ConfigURI:   uri.File("/good/project.json"),
			RootURI:     goodFolder,
			TargetFiles: []uri.URI{goodFile},
		}}},
	})

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
}

// TestCustomNonTargetHandling folds the watched/symbol/close cases for
// files the loader snapshot does not own into one table.
func TestCustomNonTargetHandling(t *testing.T) {
	setup := func(t *testing.T, client *testClient) (*Server, uri.URI, uri.URI) {
		t.Helper()
		root := uri.File("/workspace/project")
		target := uri.File("/workspace/project/api.thrift")
		nonTarget := uri.File("/workspace/project/stray.thrift")
		srv := newSyncServerWithOptions(client,
			resolvertest.Map{
				"/workspace/project/api.thrift":   []byte("struct API {}"),
				"/workspace/project/stray.thrift": []byte("struct DiskStray { 1: Missing value }"),
			}.URIs(),
			Options{ConfigSource: options.PinnedSource(nil)})
		initCustomFolders(t, srv, []uri.URI{root})
		installSnapshot(t, srv, root, WorkspaceSnapshot{Projects: []Project{{
			ConfigURI:   uri.File("/workspace/project/project.json"),
			RootURI:     root,
			TargetFiles: []uri.URI{target},
		}}})

		return srv, target, nonTarget
	}

	t.Run("watched non-target is not indexed", func(t *testing.T) {
		client := &testClient{}
		srv, target, nonTarget := setup(t, client)
		client.reset()

		require.NoError(t, srv.DidChangeWatchedFiles(t.Context(), &protocol.DidChangeWatchedFilesParams{
			Changes: []protocol.FileEvent{{URI: nonTarget, Type: protocol.FileChangeTypeChanged}},
		}))

		view, err := srv.session.ViewOf(target)
		require.NoError(t, err)
		assert.False(t, view.FileKnown(nonTarget))
		assert.Empty(t, client.last(nonTarget), "watched non-target must not publish diagnostics")
		assert.Nil(t, srv.reportFor(nonTarget), "watched non-target must not cache diagnostics")
		assert.NotContains(t, workspaceSymbolNames(t, srv), "Stray")
		assert.Contains(t, workspaceSymbolNames(t, srv), "API")
	})

	t.Run("closing an open non-target evicts it", func(t *testing.T) {
		client := &testClient{}
		srv, target, nonTarget := setup(t, client)

		openDocument(t, srv, nonTarget, "struct OpenStray { 1: Missing value }")
		view, err := srv.session.ViewOf(target)
		require.NoError(t, err)
		require.True(t, view.FileKnown(nonTarget))
		require.NotEmpty(t, client.last(nonTarget))
		assert.Contains(t, workspaceSymbolNames(t, srv), "OpenStray")

		require.NoError(t, srv.DidClose(t.Context(), &protocol.DidCloseTextDocumentParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: nonTarget},
		}))

		assert.False(t, srv.session.HasOverlay(nonTarget))
		assert.False(t, view.FileKnown(nonTarget))
		assert.Empty(t, client.last(nonTarget), "closing the final non-target owner must clear diagnostics")
		assert.Equal(t, []string{"API"}, workspaceSymbolNames(t, srv))
	})

	t.Run("symbols exclude included non-targets but definition still resolves", func(t *testing.T) {
		root := uri.File("/workspace/project")
		target := uri.File("/workspace/project/api.thrift")
		included := uri.File("/workspace/project/stray.thrift")
		srv := newSyncServerWithOptions(nil,
			resolvertest.Map{
				"/workspace/project/api.thrift":   []byte("include \"stray.thrift\"\nstruct API { 1: stray.Stray value }"),
				"/workspace/project/stray.thrift": []byte("struct Stray {}"),
			}.URIs(),
			Options{ConfigSource: options.PinnedSource(nil)})
		initCustomFolders(t, srv, []uri.URI{root})
		installSnapshot(t, srv, root, WorkspaceSnapshot{Projects: []Project{{
			ConfigURI:   uri.File("/workspace/project/project.json"),
			RootURI:     root,
			TargetFiles: []uri.URI{target},
		}}})

		view, err := srv.viewOf(target)
		require.NoError(t, err)
		require.True(t, view.FileKnown(included), "recursive analysis must retain included files in the view cache")

		names := workspaceSymbolNames(t, srv)
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
	root := uri.File("/workspace/project")
	target := uri.File("/workspace/project/api.thrift")
	unowned := uri.File("/workspace/project/stray.thrift")
	fs := &watchSpyFS{
		FileSource: store.NewMemFS(resolvertest.Map{
			"/workspace/project/api.thrift": []byte("struct Before {}"),
		}.URIs()),
		forbidden: unowned,
	}
	srv := NewServer(nil, Options{Files: fs, ConfigSource: options.PinnedSource(nil)})
	srv.diagSync = true
	_, err := srv.Initialize(t.Context(), testInitializeParams([]protocol.WorkspaceFolder{{URI: root}}))
	require.NoError(t, err)
	installSnapshot(t, srv, root, WorkspaceSnapshot{Projects: []Project{{
		ConfigURI:   uri.File("/workspace/project/project.json"),
		RootURI:     root,
		TargetFiles: []uri.URI{target},
	}}})
	fs.reads.Store(0)
	fs.contents.Store(0)

	for _, eventType := range []protocol.FileChangeType{
		protocol.FileChangeTypeChanged,
		protocol.FileChangeTypeCreated,
		protocol.FileChangeTypeDeleted,
	} {
		err := srv.DidChangeWatchedFiles(t.Context(), &protocol.DidChangeWatchedFilesParams{Changes: []protocol.FileEvent{
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
	assert.Positive(t, fs.reads.Load())
	assert.Positive(t, fs.contents.Load())
}

type watchSpyFS struct {
	store.FileSource
	forbidden uri.URI
	reads     atomic.Int32
	contents  atomic.Int32
}

func (fs *watchSpyFS) ReadFile(ctx context.Context, file uri.URI) (store.FileHandle, error) {
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
	store.FileHandle
	contents *atomic.Int32
}

func (h *watchSpyHandle) Content() ([]byte, error) {
	h.contents.Add(1)

	return h.FileHandle.Content()
}

func TestWorkspaceTargetsAreIndexedOncePerView(t *testing.T) {
	workspace := uri.File("/workspace")
	first := uri.File("/workspace/first.thrift")
	second := uri.File("/workspace/second.thrift")
	client := &testClient{}

	srv := newSyncServerWithOptions(client,
		resolvertest.Map{
			"/workspace/first.thrift":  []byte("struct First {"),
			"/workspace/second.thrift": []byte("struct Second {"),
		}.URIs(),
		Options{ConfigSource: options.PinnedSource(nil)})
	initCustomFolders(t, srv, []uri.URI{workspace})
	installSnapshot(t, srv, workspace, WorkspaceSnapshot{Projects: []Project{{
		ConfigURI:   uri.File("/workspace/project.json"),
		RootURI:     workspace,
		TargetFiles: []uri.URI{first, second},
	}}})

	view, err := srv.session.ViewOf(first)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), view.Generation())
	assert.NotEmpty(t, client.last(first))
	assert.NotEmpty(t, client.last(second))
}

func TestWorkspaceIssuesPersistAcrossFolders(t *testing.T) {
	folderA := uri.File("/workspace-a")
	folderB := uri.File("/workspace-b")
	issueURI := uri.File("/shared/project.json")
	client := &testClient{}

	srv := newSyncServerWithOptions(client, nil, Options{ConfigSource: options.PinnedSource(nil)})
	initCustomFolders(t, srv, []uri.URI{folderA, folderB})
	installSnapshots(t, srv, map[uri.URI]WorkspaceSnapshot{
		folderA: {Issues: []WorkspaceIssue{{
			URI:     issueURI,
			Message: fmt.Sprintf("issue from %s", folderA),
		}}},
		folderB: {Issues: []WorkspaceIssue{{
			URI:     issueURI,
			Message: fmt.Sprintf("issue from %s", folderB),
		}}},
	})

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
}

func TestSharedProjectViewSurvivesWorkspaceFolderRemoval(t *testing.T) {
	folderA := uri.File("/workspace-a")
	folderB := uri.File("/workspace-b")
	projectRoot := uri.File("/shared/project")

	srv := newSyncServerWithOptions(nil, nil, Options{ConfigSource: options.PinnedSource(nil)})
	initCustomFolders(t, srv, []uri.URI{folderA, folderB})
	installSnapshots(t, srv, map[uri.URI]WorkspaceSnapshot{
		folderA: {Projects: []Project{{
			ConfigURI: uri.URI(fmt.Sprintf("%s/project.json", folderA)),
			RootURI:   projectRoot,
		}}},
		folderB: {Projects: []Project{{
			ConfigURI: uri.URI(fmt.Sprintf("%s/project.json", folderB)),
			RootURI:   projectRoot,
		}}},
	})

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
}

func TestSharedRootEvictsTargetsWhenFolderLosesOwnership(t *testing.T) {
	folderA := uri.File("/workspace-a")
	folderB := uri.File("/workspace-b")
	root := uri.File("/shared/project")
	targetA := uri.File("/shared/project/a.thrift")
	targetB := uri.File("/shared/project/b.thrift")
	client := &testClient{}

	srv := newSyncServerWithOptions(client,
		resolvertest.Map{
			"/shared/project/a.thrift": []byte("struct A {}"),
			"/shared/project/b.thrift": []byte("struct B { 1: Missing value }"),
		}.URIs(),
		Options{ConfigSource: options.PinnedSource(nil)})
	initCustomFolders(t, srv, []uri.URI{folderA, folderB})
	installSnapshots(t, srv, map[uri.URI]WorkspaceSnapshot{
		folderA: {Projects: []Project{{
			ConfigURI:   uri.File(string(folderA) + "/project.json"),
			RootURI:     root,
			TargetFiles: []uri.URI{targetA},
		}}},
		folderB: {Projects: []Project{{
			ConfigURI:   uri.File(string(folderB) + "/project.json"),
			RootURI:     root,
			TargetFiles: []uri.URI{targetB},
		}}},
	})

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

	assert.False(t, view.FileKnown(targetB), "a target removed from the snapshot must leave the retained view")
	assert.Empty(t, client.last(targetB), "removing the final target owner must clear its diagnostics")
	assert.Equal(t, []string{"A"}, workspaceSymbolNames(t, srv))
}

func TestRemovingFinalCustomViewClearsDiagnostics(t *testing.T) {
	folder := uri.File("/workspace")
	root := uri.File("/workspace/project")
	target := uri.File("/workspace/project/api.thrift")
	client := &testClient{}

	srv := newSyncServerWithOptions(client,
		resolvertest.Map{"/workspace/project/api.thrift": []byte("struct A { 1: Missing value }")}.URIs(),
		Options{ConfigSource: options.PinnedSource(nil)})
	initCustomFolders(t, srv, []uri.URI{folder})
	installSnapshot(t, srv, folder, WorkspaceSnapshot{Projects: []Project{{
		ConfigURI:   uri.File("/workspace/project.json"),
		RootURI:     root,
		TargetFiles: []uri.URI{target},
	}}})
	require.NotEmpty(t, client.last(target))

	require.NoError(t, srv.DidChangeWorkspaceFolders(t.Context(), &protocol.DidChangeWorkspaceFoldersParams{
		Event: protocol.WorkspaceFoldersChangeEvent{
			Removed: []protocol.WorkspaceFolder{{URI: folder}},
		},
	}))

	assert.Empty(t, srv.session.Views())
	assert.Empty(t, client.last(target), "removing the final view must publish a clear diagnostic set")
}

func TestCustomOpenOverlayMovesToNewOwner(t *testing.T) {
	outerFolder := uri.File("/workspace")
	innerFolder := uri.File("/workspace/service")
	file := uri.File("/workspace/service/api.thrift")

	srv := newSyncServerWithOptions(nil,
		resolvertest.Map{"/workspace/service/api.thrift": []byte("struct DiskVersion {}")}.URIs(),
		Options{ConfigSource: options.PinnedSource(nil)})
	initCustomFolders(t, srv, []uri.URI{outerFolder})
	installSnapshot(t, srv, outerFolder, WorkspaceSnapshot{Projects: []Project{{
		ConfigURI:   uri.File("/workspace/project.json"),
		RootURI:     outerFolder,
		TargetFiles: []uri.URI{file},
	}}})

	openDocument(t, srv, file, "struct OverlayVersion {}")
	outerView, err := srv.session.ViewOf(file)
	require.NoError(t, err)
	assert.Equal(t, outerFolder, outerView.Folder())

	require.NoError(t, srv.DidChangeWorkspaceFolders(t.Context(), &protocol.DidChangeWorkspaceFoldersParams{
		Event: protocol.WorkspaceFoldersChangeEvent{
			Added: []protocol.WorkspaceFolder{{URI: innerFolder}},
		},
	}))
	installSnapshot(t, srv, innerFolder, WorkspaceSnapshot{Projects: []Project{{
		ConfigURI: uri.File("/workspace/service/project.json"),
		RootURI:   innerFolder,
	}}})

	innerView, err := srv.session.ViewOf(file)
	require.NoError(t, err)
	assert.Equal(t, innerFolder, innerView.Folder())
	assert.False(t, outerView.FileKnown(file))
	parsed, err := innerView.Parse(t.Context(), file)
	require.NoError(t, err)
	assert.Contains(t, parsed.Definitions(), "OverlayVersion")
	assert.True(t, srv.session.HasOverlay(file))
}

// TestWorkspaceModelRouting pins the pure routing math without any server:
// most-specific roots win, targets fall back to their declared root, and
// open documents join their containing root.
func TestWorkspaceModelRouting(t *testing.T) {
	outer := uri.File("/workspace")
	inner := uri.File("/workspace/service")
	outerFile := uri.File("/workspace/root.thrift")
	innerFile := uri.File("/workspace/service/api.thrift")
	external := uri.File("/dependencies/shared.thrift")

	snapshots := map[uri.URI]WorkspaceSnapshot{
		outer: {Projects: []Project{
			{ConfigURI: uri.File("/workspace/project.json"), RootURI: outer, TargetFiles: []uri.URI{outerFile, innerFile}},
			{ConfigURI: uri.File("/workspace/service/project.json"), RootURI: inner, TargetFiles: []uri.URI{external}},
		}},
	}

	model := workspaceModelOf(snapshots)

	root, ok := model.rootFor(innerFile)
	require.True(t, ok)
	assert.Equal(t, inner, root, "most-specific containing root wins")

	root, ok = model.rootFor(external)
	require.True(t, ok)
	assert.Equal(t, inner, root, "external target falls back to its declared root")

	owner, ok := model.ownerOf(innerFile, nil)
	require.True(t, ok)
	assert.Equal(t, inner, owner)

	_, ok = model.ownerOf(uri.File("/workspace/stray.thrift"), nil)
	assert.False(t, ok, "unowned non-target without an open document has no owner")

	owner, ok = model.ownerOf(uri.File("/workspace/stray.thrift"), map[uri.URI]struct{}{uri.File("/workspace/stray.thrift"): {}})
	require.True(t, ok)
	assert.Equal(t, outer, owner, "an open document joins its containing root")

	assert.ElementsMatch(t, []uri.URI{outerFile}, model.ownedFiles(outer, nil))
	assert.ElementsMatch(t, []uri.URI{innerFile, external}, model.ownedFiles(inner, nil))
}

// TestValidateProject pins snapshot validation without any server: bad
// URIs and configs are rejected, and rejection carries a workspace issue.
func TestValidateProject(t *testing.T) {
	require.NoError(t, validateProject(Project{
		ConfigURI:   uri.File("/workspace/project.json"),
		RootURI:     uri.File("/workspace/service"),
		TargetFiles: []uri.URI{uri.File("/workspace/service/api.thrift")},
	}))

	assert.Error(t, validateProject(Project{
		ConfigURI: uri.File("/workspace/project.json"),
		RootURI:   uri.URI("not a URI"),
	}))

	assert.Error(t, validateProject(Project{
		RootURI: uri.File("/workspace/service"),
	}), "missing config URI is rejected")

	snap := validateWorkspaceSnapshot(uri.File("/workspace"), WorkspaceSnapshot{Projects: []Project{
		{
			ConfigURI:   uri.File("/workspace/bad.json"),
			RootURI:     uri.URI("not a URI"),
			TargetFiles: []uri.URI{uri.File("/workspace/api.thrift")},
		},
		{
			ConfigURI:   uri.File("/workspace/good.json"),
			RootURI:     uri.File("/workspace/service"),
			TargetFiles: []uri.URI{uri.File("/workspace/service/api.thrift")},
		},
	}})
	require.Len(t, snap.Projects, 1)
	assert.Equal(t, uri.File("/workspace/service"), snap.Projects[0].RootURI)
	require.Len(t, snap.Issues, 1)
	assert.Equal(t, uri.File("/workspace/bad.json"), snap.Issues[0].URI)
}
