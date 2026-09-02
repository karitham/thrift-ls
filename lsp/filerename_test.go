package lsp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// Test_DidRenameFiles_DropsState pins the state cleanup: the old URI's
// editor overlay is forgotten (so the watcher guard lets this rename's
// delete/create events through), and the includer's include edge
// re-resolves to the new location.
func Test_DidRenameFiles_DropsState(t *testing.T) {
	dir := "/ws"
	libURI := uri.File(dir + "/lib.thrift")
	mainURI := uri.File(dir + "/main.thrift")

	srv := newTestServer(&testClient{})

	openDocument(t, srv, libURI, "struct Lib {}\n")
	openDocument(t, srv, mainURI, "include \"lib.thrift\"\nstruct M { 1: lib.L l }\n")

	require.True(t, srv.session.HasOverlay(libURI), "setup: opened documents have overlays")

	// The client applies the willRename edits before performing the
	// rename; simulate that by updating the editor buffer first.
	require.NoError(t, srv.DidChange(t.Context(), &protocol.DidChangeTextDocumentParams{
		TextDocument: protocol.VersionedTextDocumentIdentifier{
			TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: mainURI},
			Version:                2,
		},
		ContentChanges: []protocol.TextDocumentContentChangeEvent{
			&protocol.TextDocumentContentChangeWholeDocument{
				Text: "include \"lib2.thrift\"\nstruct M { 1: lib2.L l }\n",
			},
		},
	}))

	require.NoError(t, srv.DidRenameFiles(t.Context(), &protocol.RenameFilesParams{
		Files: []protocol.FileRename{
			{OldURI: string(libURI), NewURI: string(uri.File(dir + "/lib2.thrift"))},
		},
	}))

	assert.False(t, srv.session.HasOverlay(libURI), "the old location must not keep a phantom overlay")

	view, err := srv.session.ViewOf(mainURI)
	require.NoError(t, err)

	includes := view.Includes(mainURI)
	require.Len(t, includes, 1)
	assert.Equal(t, string(uri.File(dir+"/lib2.thrift")), string(includes[0]),
		"the includer's edge must re-resolve to the new location")
}

// Test_WillRenameFiles drives the server handler: thrift renames return a
// workspace edit rewriting every include of the renamed file, non-thrift
// renames return none.
func Test_WillRenameFiles(t *testing.T) {
	tests := []struct {
		name string

		// documents are opened before the rename: file name and content.
		documents []struct{ name, content string }

		oldName string
		newName string

		// wantEdits is keyed by the including file's name; nil expects no
		// workspace edit at all.
		wantEdits map[string][]protocol.TextEdit
	}{
		{
			name: "renaming an included file retargets its includers",
			documents: []struct{ name, content string }{
				{"lib.thrift", "struct Lib {}\n"},
				{"main.thrift", "include \"lib.thrift\"\nstruct M { 1: lib.L l }\n"},
			},
			oldName: "lib.thrift",
			newName: "lib2.thrift",
			wantEdits: map[string][]protocol.TextEdit{
				"main.thrift": {{
					Range: protocol.Range{
						Start: protocol.Position{Line: 0, Character: 8},
						End:   protocol.Position{Line: 0, Character: 20},
					},
					NewText: "\"lib2.thrift\"",
				}},
			},
		},
		{
			name:      "renaming a non-thrift file yields no edit",
			documents: nil,
			oldName:   "notes.txt",
			newName:   "memo.txt",
			wantEdits: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := "/ws"
			srv := newTestServer(&testClient{})

			for _, d := range tt.documents {
				openDocument(t, srv, uri.File(dir+"/"+d.name), d.content)
			}

			edit, err := srv.WillRenameFiles(t.Context(), &protocol.RenameFilesParams{
				Files: []protocol.FileRename{
					{
						OldURI: string(uri.File(dir + "/" + tt.oldName)),
						NewURI: string(uri.File(dir + "/" + tt.newName)),
					},
				},
			})
			require.NoError(t, err)

			if tt.wantEdits == nil {
				assert.Nil(t, edit)

				return
			}

			require.NotNil(t, edit)
			require.Len(t, edit.Changes, len(tt.wantEdits))

			for name, want := range tt.wantEdits {
				assert.Equal(t, want, edit.Changes[uri.File(dir+"/"+name)])
			}
		})
	}
}
