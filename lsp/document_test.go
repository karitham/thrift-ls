package lsp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/resolver/resolvertest"
)

const (
	docAContent = "include \"B.thrift\"\nstruct A {\n\t1: required B b\n}\n"
	docBContent = "struct B {\n\t1: required string x\n}\n"
)

// TestDocumentSync pins open/change bookkeeping: content lands in the
// session with its version, and a whole-document change replaces it.
func TestDocumentSync(t *testing.T) {
	for _, tt := range []struct {
		name        string
		initial     string
		change      string
		wantVersion int
		wantContent string
	}{
		{
			name:        "open stores content at version zero",
			initial:     "struct Test { 1: required string Name }",
			wantVersion: 0,
			wantContent: "struct Test { 1: required string Name }",
		},
		{
			name:        "change replaces content and bumps the version",
			initial:     "struct Test { 1: required string Name }",
			change:      "struct Test { 1: required string Name, 2: optional i32 Age }",
			wantVersion: 1,
			wantContent: "struct Test { 1: required string Name, 2: optional i32 Age }",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			file := uri.File("/ws/file.thrift")
			srv := newMemServer(nil)

			openDocument(t, srv, file, tt.initial)

			if tt.change != "" {
				require.NoError(t, srv.DidChange(ctx, &protocol.DidChangeTextDocumentParams{
					TextDocument: protocol.VersionedTextDocumentIdentifier{
						TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: file},
						Version:                1,
					},
					ContentChanges: []protocol.TextDocumentContentChangeEvent{
						&protocol.TextDocumentContentChangeWholeDocument{Text: tt.change},
					},
				}))
			}

			fh, err := srv.session.ReadFile(ctx, file)
			require.NoError(t, err)
			assert.Equal(t, tt.wantVersion, int(fh.Version()))
			got, err := fh.Content()
			require.NoError(t, err)
			assert.Equal(t, tt.wantContent, string(got))
		})
	}
}

// TestFileChangeFromLSPDidChange pins whole-document sync semantics: the
// server advertises TextDocumentSyncKindFull, so whole-document events map
// one-to-one and incremental (partial) events are skipped.
func TestFileChangeFromLSPDidChange(t *testing.T) {
	tests := []struct {
		name        string
		content     []protocol.TextDocumentContentChangeEvent
		wantContent []byte
		want        int
	}{
		{
			name: "whole document",
			content: []protocol.TextDocumentContentChangeEvent{
				&protocol.TextDocumentContentChangeWholeDocument{Text: "struct HTT {}"},
			},
			wantContent: []byte("struct HTT {}"),
			want:        1,
		},
		{
			name: "incremental change is skipped",
			content: []protocol.TextDocumentContentChangeEvent{
				&protocol.TextDocumentContentChangePartial{
					Range: protocol.Range{
						Start: protocol.Position{Line: 0, Character: 7},
						End:   protocol.Position{Line: 0, Character: 10},
					},
					Text: "HoukagoTeaTime",
				},
			},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changes := FileChangeFromLSPDidChange(&protocol.DidChangeTextDocumentParams{
				TextDocument: protocol.VersionedTextDocumentIdentifier{
					TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: "file:///tmp/song.thrift"},
				},
				ContentChanges: tt.content,
			})

			require.Len(t, changes, tt.want)

			if tt.want == 1 {
				assert.Equal(t, tt.wantContent, changes[0].Content)
			}
		})
	}
}

// TestDependentDiagnosticsRepublish covers the user-visible invariant:
// editing an included file re-publishes diagnostics for its dependents,
// whether the edit arrives through the editor or from disk.
func TestDependentDiagnosticsRepublish(t *testing.T) {
	for _, tt := range []struct {
		name string
		// viaDisk delivers the edit as a watched-files event instead of DidChange.
		viaDisk bool
	}{
		{name: "editor edit republishes the dependent", viaDisk: false},
		{name: "disk edit refreshes content and republishes", viaDisk: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dependent := uri.File("/ws/strike_rouge.thrift")
			included := uri.File("/ws/federation.gundam.thrift")
			disk := "struct Gundam {\n\t1: required string Name\n}"
			overlayDependent := "include \"federation.gundam.thrift\"\n\nexception BayFull {\n\t1: string message\n}"

			files := map[uri.URI][]byte{included: []byte(disk)}
			client := &testClient{}
			srv := newSyncServerWithOptions(client, files, Options{})

			openDocument(t, srv, dependent, overlayDependent)
			if !tt.viaDisk {
				openDocument(t, srv, included, disk)
			}
			client.reset()

			changed := "struct Gundam {\n\t1: required string Name,\n\t2: optional i32 SerialNumber\n}"
			if tt.viaDisk {
				files[included] = []byte(changed)
				require.NoError(t, srv.DidChangeWatchedFiles(t.Context(), &protocol.DidChangeWatchedFilesParams{
					Changes: []protocol.FileEvent{{URI: included, Type: protocol.FileChangeTypeChanged}},
				}))
			} else {
				require.NoError(t, srv.DidChange(t.Context(), &protocol.DidChangeTextDocumentParams{
					TextDocument: protocol.VersionedTextDocumentIdentifier{
						TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: included},
						Version:                1,
					},
					ContentChanges: []protocol.TextDocumentContentChangeEvent{
						&protocol.TextDocumentContentChangeWholeDocument{Text: changed},
					},
				}))
			}

			assert.GreaterOrEqual(t, client.count(included), 1, "changed file gets diagnostics")
			assert.GreaterOrEqual(t, client.count(dependent), 1, "dependent of changed file gets diagnostics")

			if tt.viaDisk {
				fh, err := srv.session.ReadFile(t.Context(), included)
				require.NoError(t, err)
				content, err := fh.Content()
				require.NoError(t, err)
				assert.Contains(t, string(content), "SerialNumber")
			}
		})
	}
}

// TestOverlayLifecycle pins overlay authority: closing drops the overlay
// so reads fall back to disk, and disk events for open documents are
// ignored while the overlay lives.
func TestOverlayLifecycle(t *testing.T) {
	t.Run("close drops the overlay and keeps include edges", func(t *testing.T) {
		disk := "struct Gundam {\n\t1: required string Name\n}"
		overlay := "struct Gundam {\n\t1: required string Name,\n\t2: optional i32 SerialNumber\n}"
		dependentText := "include \"federation.gundam.thrift\"\n\nexception BayFull {\n\t1: string message\n}"
		dependent := uri.File("/ws/strike_rouge.thrift")
		included := uri.File("/ws/federation.gundam.thrift")

		files := resolvertest.Map{
			"/ws/federation.gundam.thrift": []byte(disk),
			"/ws/strike_rouge.thrift":      []byte(dependentText),
		}.URIs()
		srv := newSyncServerWithOptions(nil, files, Options{})
		openDocument(t, srv, dependent, dependentText)
		openDocument(t, srv, included, overlay)

		ctx := t.Context()
		fh, err := srv.session.ReadFile(ctx, included)
		require.NoError(t, err)
		content, err := fh.Content()
		require.NoError(t, err)
		assert.Equal(t, overlay, string(content))

		require.NoError(t, srv.DidClose(ctx, &protocol.DidCloseTextDocumentParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: included},
		}))

		fh, err = srv.session.ReadFile(ctx, included)
		require.NoError(t, err)
		content, err = fh.Content()
		require.NoError(t, err)
		assert.Equal(t, disk, string(content))

		view, err := srv.session.ViewOf(dependent)
		require.NoError(t, err)
		_, err = view.Parse(ctx, dependent)
		require.NoError(t, err)
		assert.Equal(t, []uri.URI{dependent}, view.Dependents(included))
	})

	t.Run("disk events for open files are ignored", func(t *testing.T) {
		disk := "struct Gundam {\n\t1: required string Name\n}"
		overlay := "struct Gundam {\n\t1: required string Name,\n\t2: optional i32 SerialNumber\n}"
		file := uri.File("/ws/federation.gundam.thrift")

		files := resolvertest.Map{"/ws/federation.gundam.thrift": []byte(disk)}.URIs()
		srv := newSyncServerWithOptions(nil, files, Options{})
		openDocument(t, srv, file, overlay)

		files[file] = []byte(disk)
		require.NoError(t, srv.DidChangeWatchedFiles(t.Context(), &protocol.DidChangeWatchedFilesParams{
			Changes: []protocol.FileEvent{{URI: file, Type: protocol.FileChangeTypeChanged}},
		}))

		fh, err := srv.session.ReadFile(t.Context(), file)
		require.NoError(t, err)
		content, err := fh.Content()
		require.NoError(t, err)
		assert.Equal(t, overlay, string(content))
	})
}

// TestMissingIncludeClears folds the three include-creation paths into one
// table: A includes a missing B, then B appears via the editor, via disk,
// or via a full session with watcher registration. A's diagnostics clear
// in every case.
func TestMissingIncludeClears(t *testing.T) {
	for _, tt := range []struct {
		name string
		// fullSession runs initialize plus watcher registration first and
		// asserts the thrift watcher is registered.
		fullSession bool
		// viaDisk creates B on disk with a watched-files event.
		viaDisk bool
	}{
		{name: "created in editor"},
		{name: "created on disk", viaDisk: true},
		{name: "full session registers watcher then clears", viaDisk: true, fullSession: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			aURI := uri.File("/ws/A.thrift")
			bURI := uri.File("/ws/B.thrift")

			files := map[uri.URI][]byte{}
			client := &testClient{}
			srv := newSyncServerWithOptions(client, files, Options{})

			if tt.fullSession {
				_, err := srv.Initialize(t.Context(), &protocol.InitializeParams{})
				require.NoError(t, err)
				srv.registerFileWatcher(t.Context())
				require.NotEmpty(t, client.watchers(), "file watcher must be registered on Initialized")
				assert.Contains(t, client.watchers(), "**/*.thrift")
			}

			openDocument(t, srv, aURI, docAContent)
			assert.Contains(t, diagMessages(client.last(aURI)), "field type doesn't exist")
			client.reset()

			if tt.viaDisk {
				files[bURI] = []byte(docBContent)
				require.NoError(t, srv.DidChangeWatchedFiles(t.Context(), &protocol.DidChangeWatchedFilesParams{
					Changes: []protocol.FileEvent{{URI: bURI, Type: protocol.FileChangeTypeCreated}},
				}))
			} else {
				openDocument(t, srv, bURI, docBContent)
			}

			assert.Empty(t, client.last(aURI), "A's diagnostics must clear once B exists")
		})
	}
}
