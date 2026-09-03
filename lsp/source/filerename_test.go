package source

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/store"
)

// litEdit is the expected edit of a whole include literal on one line.
func litEdit(colStart, colEnd int, text string) protocol.TextEdit {
	return protocol.TextEdit{
		Range: protocol.Range{
			Start: protocol.Position{Line: 0, Character: uint32(colStart)},
			End:   protocol.Position{Line: 0, Character: uint32(colEnd)},
		},
		NewText: text,
	}
}

func openDoc(u, content string) *store.FileChange {
	return &store.FileChange{
		URI:     uri.URI(u),
		Version: 0,
		Content: []byte(content),
		From:    store.FileChangeTypeDidOpen,
	}
}

// Test_RenameFileEdits rewrites every include literal naming the renamed
// file. Flat same-directory includes swap their file-name segment,
// relative includes reaching the renamed file become includer-relative
// paths, and includes resolved through a -I style include path keep
// their lookup shape.
func Test_RenameFileEdits(t *testing.T) {
	tests := []struct {
		name   string
		files  []*store.FileChange
		paths  []string // extra include paths, like --I
		oldURI string
		newURI string

		// want is the complete expected edit set.
		want map[uri.URI][]protocol.TextEdit
	}{
		{
			name: "flat and relative includes both rewrite",
			files: []*store.FileChange{
				openDoc("file:///tmp/ren/shared.thrift", "struct Shared {}\n"),
				openDoc("file:///tmp/ren/a.thrift", "include \"shared.thrift\"\nstruct A { 1: shared.X x }\n"),
				openDoc("file:///tmp/ren/sub/b.thrift", "include \"../shared.thrift\"\nstruct B { 1: shared.X x }\n"),
			},
			oldURI: "file:///tmp/ren/shared.thrift",
			newURI: "file:///tmp/ren/renamed.thrift",
			want: map[uri.URI][]protocol.TextEdit{
				"file:///tmp/ren/a.thrift": {litEdit(8, 23, "\"renamed.thrift\"")},
				"file:///tmp/ren/sub/b.thrift": {
					litEdit(8, 26, "\"../renamed.thrift\""),
				},
			},
		},
		{
			name: "a file nobody includes yields no edits",
			files: []*store.FileChange{
				openDoc("file:///tmp/solo/lonely.thrift", "struct Lonely {}\n"),
			},
			oldURI: "file:///tmp/solo/lonely.thrift",
			newURI: "file:///tmp/solo/moved.thrift",
			want:   map[uri.URI][]protocol.TextEdit{},
		},
		{
			name: "a move between directories under one include root rewrites the whole tail",
			files: []*store.FileChange{
				openDoc("file:///tmp/inc/vendor/base.thrift", "struct Base {}\n"),
				openDoc("file:///tmp/proj/main.thrift", "include \"vendor/base.thrift\"\nstruct M { 1: vendor.B b }\n"),
			},
			paths:  []string{"/tmp/inc"},
			oldURI: "file:///tmp/inc/vendor/base.thrift",
			newURI: "file:///tmp/inc/lib/renamed.thrift",
			want: map[uri.URI][]protocol.TextEdit{
				"file:///tmp/proj/main.thrift": {litEdit(8, 28, "\"lib/renamed.thrift\"")},
			},
		},
		{
			name: "an include resolved through an include path swaps only its base name",
			files: []*store.FileChange{
				openDoc("file:///tmp/inc/vendor.thrift", "struct Vendor {}\n"),
				openDoc("file:///tmp/proj/main.thrift", "include \"vendor.thrift\"\nstruct M { 1: vendor.V v }\n"),
			},
			paths:  []string{"/tmp/inc"},
			oldURI: "file:///tmp/inc/vendor.thrift",
			newURI: "file:///tmp/inc/renamed.thrift",
			want: map[uri.URI][]protocol.TextEdit{
				"file:///tmp/proj/main.thrift": {litEdit(8, 23, "\"renamed.thrift\"")},
			},
		},
		{
			name: "a self-include rewrites in place",
			files: []*store.FileChange{
				openDoc("file:///tmp/self/loop.thrift", "include \"loop.thrift\"\nstruct L {}\n"),
			},
			oldURI: "file:///tmp/self/loop.thrift",
			newURI: "file:///tmp/self/loop2.thrift",
			want: map[uri.URI][]protocol.TextEdit{
				"file:///tmp/self/loop.thrift": {litEdit(8, 21, "\"loop2.thrift\"")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := store.BuildViewForTestWithPaths(tt.paths, tt.files)

			got, err := RenameFileEdits(t.Context(), view, uri.URI(tt.oldURI), uri.URI(tt.newURI))
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// Test_RenamedIncludeText covers the literal rewriter's quote handling.
func Test_RenamedIncludeText(t *testing.T) {
	tests := []struct {
		name        string
		literal     string
		includerDir string
		want        string
	}{
		{
			name:        "double quotes survive",
			literal:     "\"shared.thrift\"",
			includerDir: "/w",
			want:        "\"renamed.thrift\"",
		},
		{
			name:        "single quotes survive",
			literal:     "'shared.thrift'",
			includerDir: "/w",
			want:        "'renamed.thrift'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := renamedIncludeText(tt.literal, "/w/shared.thrift", "/w/renamed.thrift", tt.includerDir, nil)
			require.True(t, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}
