package source

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/store"
)

// Test_StructuredAnnotationNavigation covers the cursor features on
// structured annotation names, which resolve like any other type
// reference: go-to-definition from @Name, find-references from the
// definition (the annotation usage included), and rename (definition and
// usage rewritten together).
func Test_StructuredAnnotationNavigation(t *testing.T) {
	content := `struct Naming {
  1: optional string ns,
}

@Naming{'ns': 'x'}
struct S {
  1: i32 a
}
`

	view := store.BuildViewForTest([]*store.FileChange{
		{
			URI:     "file:///tmp/anno.thrift",
			Version: 0,
			Content: []byte(content),
			From:    store.FileChangeTypeDidOpen,
		},
	})

	file := uri.URI("file:///tmp/anno.thrift")

	// The definition's identifier and the annotation's usage, located by
	// their source text. The content is ASCII, so byte columns are UTF-16
	// columns.
	defRange := textRange(t, content, "struct Naming", "Naming")
	useRange := textRange(t, content, "@Naming", "Naming")

	t.Run("locations", func(t *testing.T) {
		tests := []struct {
			name  string
			probe func(context.Context, *store.View, uri.URI, protocol.Position) ([]protocol.Location, error)
			pos   protocol.Position
			want  []protocol.Location
		}{
			{
				name:  "definition from the annotation name",
				probe: Definition,
				pos:   inside(useRange),
				want:  []protocol.Location{{URI: file, Range: defRange}},
			},
			{
				name:  "type definition from the annotation name",
				probe: TypeDefinition,
				pos:   inside(useRange),
				want:  []protocol.Location{{URI: file, Range: defRange}},
			},
			{
				name:  "references from the definition include the annotation use",
				probe: Reference,
				pos:   inside(defRange),
				want:  []protocol.Location{{URI: file, Range: useRange}},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				got, err := tt.probe(t.Context(), view, file, tt.pos)
				require.NoError(t, err)

				locs := make([]protocol.Location, len(got))
				for i, l := range got {
					locs[i] = protocol.Location{URI: l.URI, Range: l.Range}
				}

				assert.ElementsMatch(t, tt.want, locs)
			})
		}
	})

	t.Run("hover", func(t *testing.T) {
		got, err := Hover(t.Context(), view, file, inside(useRange))
		require.NoError(t, err)
		assert.Contains(t, got, "struct Naming")
	})

	t.Run("rename", func(t *testing.T) {
		tests := []struct {
			name    string
			prepare bool // probe PrepareRename instead of Rename
			pos     protocol.Position
			newName string
			want    []protocol.Range
		}{
			{
				name:    "prepare rename reports the annotation name",
				prepare: true,
				pos:     inside(useRange),
				want:    []protocol.Range{useRange},
			},
			{
				name:    "rename rewrites the definition and the annotation use",
				pos:     inside(defRange),
				newName: "Label",
				want:    []protocol.Range{defRange, useRange},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if tt.prepare {
					rg, err := PrepareRename(t.Context(), view, file, tt.pos)
					require.NoError(t, err)
					require.NotNil(t, rg)

					assert.ElementsMatch(t, tt.want, []protocol.Range{*rg})

					return
				}

				edit, err := Rename(t.Context(), view, file, tt.pos, tt.newName)
				require.NoError(t, err)
				require.NotNil(t, edit)

				changes := edit.Changes[file]
				got := make([]protocol.Range, len(changes))
				for i, c := range changes {
					got[i] = c.Range
				}

				assert.ElementsMatch(t, tt.want, got)
			})
		}
	})
}

// textRange returns the range of sub's first occurrence after anchor in
// src. The sources this helper reads are ASCII, so byte columns are
// UTF-16 columns.
func textRange(t *testing.T, src, anchor, sub string) protocol.Range {
	t.Helper()

	at := strings.Index(src, anchor)
	require.NotEqual(t, -1, anchor, "anchor %q not in source", anchor)

	off := strings.Index(src[at:], sub)
	require.NotEqual(t, -1, sub, "sub %q not after anchor", sub)
	off += at

	line := strings.Count(src[:off], "\n")
	lineStart := strings.LastIndex(src[:off], "\n") + 1

	start := protocol.Position{Line: uint32(line), Character: uint32(off - lineStart)}

	return protocol.Range{Start: start, End: protocol.Position{Line: start.Line, Character: start.Character + uint32(len(sub))}}
}

// inside returns a cursor position within r: one rune in from the start.
func inside(r protocol.Range) protocol.Position {
	return protocol.Position{Line: r.Start.Line, Character: r.Start.Character + 1}
}
