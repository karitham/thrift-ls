package lsp

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

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
			dir := t.TempDir()
			srv := newTestServer(&diagClient{})

			for _, d := range tt.documents {
				openDocument(t, srv, uri.File(filepath.Join(dir, d.name)), d.content)
			}

			edit, err := srv.WillRenameFiles(t.Context(), &protocol.RenameFilesParams{
				Files: []protocol.FileRename{
					{
						OldURI: string(uri.File(filepath.Join(dir, tt.oldName))),
						NewURI: string(uri.File(filepath.Join(dir, tt.newName))),
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
				assert.Equal(t, want, edit.Changes[uri.File(filepath.Join(dir, name))])
			}
		})
	}
}
