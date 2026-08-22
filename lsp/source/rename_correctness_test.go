package source

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/cache"
)

// TestRenameTransitiveInclude pins that a definition reached through a
// chain of includes (app → mid → base) is renamed everywhere it is
// referenced — including in files that include it only transitively.
func TestRenameTransitiveInclude(t *testing.T) {
	view := cache.BuildViewForTest([]*cache.FileChange{
		{
			URI:     "file:///tmp/base.thrift",
			Version: 0,
			Content: []byte("struct User { 1: i32 id }\n"),
			From:    cache.FileChangeTypeDidOpen,
		},
		{
			URI:     "file:///tmp/mid.thrift",
			Version: 0,
			Content: []byte("include \"base.thrift\"\n"),
			From:    cache.FileChangeTypeDidOpen,
		},
		{
			URI:     "file:///tmp/app.thrift",
			Version: 0,
			Content: []byte("include \"mid.thrift\"\nstruct S { 1: User u }\n"),
			From:    cache.FileChangeTypeDidOpen,
		},
	})

	edit, err := Rename(t.Context(), view, "file:///tmp/base.thrift", protocol.Position{Line: 0, Character: 7}, "Account")
	require.NoError(t, err)

	assert.Equal(t, []protocol.TextEdit{{
		Range:   protocol.Range{Start: protocol.Position{Line: 0, Character: 7}, End: protocol.Position{Line: 0, Character: 11}},
		NewText: "Account",
	}}, edit.Changes["file:///tmp/base.thrift"], "the definition itself")

	assert.Equal(t, []protocol.TextEdit{{
		Range:   protocol.Range{Start: protocol.Position{Line: 1, Character: 14}, End: protocol.Position{Line: 1, Character: 18}},
		NewText: "Account",
	}}, edit.Changes["file:///tmp/app.thrift"], "the transitive reference in app.thrift must be renamed")
}

// TestRenameResolutionMatched pins that renaming a definition leaves
// references to a same-named definition from another file untouched:
// matches are resolved to their actual definition, not matched by name.
func TestRenameResolutionMatched(t *testing.T) {
	view := cache.BuildViewForTest([]*cache.FileChange{
		{
			URI:     "file:///tmp/base.thrift",
			Version: 0,
			Content: []byte("struct User { 1: i32 id }\n"),
			From:    cache.FileChangeTypeDidOpen,
		},
		{
			URI:     "file:///tmp/app.thrift",
			Version: 0,
			Content: []byte("include \"base.thrift\"\nstruct User { 1: string name }\nstruct S { 1: base.User x, 2: User y }\n"),
			From:    cache.FileChangeTypeDidOpen,
		},
	})

	// Rename app.thrift's own User (line 1, the definition).
	edit, err := Rename(t.Context(), view, "file:///tmp/app.thrift", protocol.Position{Line: 1, Character: 7}, "Member")
	require.NoError(t, err)

	// Only the unqualified reference and the definition change; the
	// qualified reference to base.thrift's User does not.
	assert.Equal(t, []protocol.TextEdit{
		{Range: protocol.Range{Start: protocol.Position{Line: 2, Character: 30}, End: protocol.Position{Line: 2, Character: 34}}, NewText: "Member"},
		{Range: protocol.Range{Start: protocol.Position{Line: 1, Character: 7}, End: protocol.Position{Line: 1, Character: 11}}, NewText: "Member"},
	}, edit.Changes["file:///tmp/app.thrift"])

	assert.Empty(t, edit.Changes["file:///tmp/base.thrift"], "the same-named definition in base.thrift is untouched")
}

// TestRenameEnumValueResolutionMatched pins that renaming an enum value
// only touches references that resolve to it: "colors.Palette.RED" must
// survive a rename of the local enum's RED.
func TestRenameEnumValueResolutionMatched(t *testing.T) {
	view := cache.BuildViewForTest([]*cache.FileChange{
		{
			URI:     "file:///tmp/colors.thrift",
			Version: 0,
			Content: []byte("enum Palette { RED }\n"),
			From:    cache.FileChangeTypeDidOpen,
		},
		{
			URI:     "file:///tmp/main.thrift",
			Version: 0,
			Content: []byte("include \"colors.thrift\"\nenum Local { RED }\nstruct S {\n  1: i32 a = Local.RED,\n  2: i32 b = colors.Palette.RED,\n}\n"),
			From:    cache.FileChangeTypeDidOpen,
		},
	})

	// Cursor on the local RED definition (line 1, char 13).
	edit, err := Rename(t.Context(), view, "file:///tmp/main.thrift", protocol.Position{Line: 1, Character: 13}, "CRIMSON")
	require.NoError(t, err)

	var got []string
	for _, te := range edit.Changes["file:///tmp/main.thrift"] {
		got = append(got, te.NewText)
	}

	// The Local.RED qualifier keeps the enum name, and the definition
	// changes; colors.Palette.RED does not.
	assert.Equal(t, []string{"Local.CRIMSON", "CRIMSON"}, got)
}

// TestRenameEnumResolutionMatched pins that renaming an enum only touches
// value references qualified with that enum: same-named enums in other
// files are left alone.
func TestRenameEnumResolutionMatched(t *testing.T) {
	view := cache.BuildViewForTest([]*cache.FileChange{
		{
			URI:     "file:///tmp/colors.thrift",
			Version: 0,
			Content: []byte("enum Color { RED }\n"),
			From:    cache.FileChangeTypeDidOpen,
		},
		{
			URI:     "file:///tmp/main.thrift",
			Version: 0,
			Content: []byte("include \"colors.thrift\"\nenum Color { BLUE }\nstruct S {\n  1: i32 a = Color.BLUE,\n  2: i32 b = colors.Color.RED,\n}\n"),
			From:    cache.FileChangeTypeDidOpen,
		},
	})

	// Cursor on the local Color definition (line 1, char 5).
	edit, err := Rename(t.Context(), view, "file:///tmp/main.thrift", protocol.Position{Line: 1, Character: 5}, "Hue")
	require.NoError(t, err)

	var got []string
	for _, te := range edit.Changes["file:///tmp/main.thrift"] {
		got = append(got, te.NewText)
	}

	// The Color.BLUE qualifier and the definition change; colors.Color.RED
	// is untouched.
	assert.Equal(t, []string{"Hue", "Hue"}, got)
	assert.Empty(t, edit.Changes["file:///tmp/colors.thrift"])
}
