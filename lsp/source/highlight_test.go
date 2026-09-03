package source

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/store"
)

// TestHighlightSameFile pins document highlighting: the identifier at the
// cursor highlights its same-file references only.
func TestHighlightSameFile(t *testing.T) {
	tests := []struct {
		name      string
		files     []*store.FileChange
		pos       protocol.Position // cursor position in the first file
		wantLines []uint32          // highlighted lines in the first file
	}{
		{
			name: "type name highlights definition and usages",
			files: []*store.FileChange{
				{URI: "file:///tmp/main.thrift", Version: 0, Content: []byte(`struct Gundam {
	1: required string Name
}

struct StrikeRouge {
	1: required Gundam pack
	2: optional Gundam beamSaber
}`), From: store.FileChangeTypeDidOpen},
			},
			pos:       protocol.Position{Line: 0, Character: 7},
			wantLines: []uint32{0, 5, 6},
		},
		{
			name: "usage highlights the definition too",
			files: []*store.FileChange{
				{URI: "file:///tmp/main.thrift", Version: 0, Content: []byte(`struct Gundam {
	1: required string Name
}

struct StrikeRouge {
	1: required Gundam pack
}`), From: store.FileChangeTypeDidOpen},
			},
			pos:       protocol.Position{Line: 5, Character: 14},
			wantLines: []uint32{0, 5},
		},
		{
			name: "cross-file references are excluded",
			files: []*store.FileChange{
				{URI: "file:///tmp/gundam.thrift", Version: 0, Content: []byte(`struct Gundam {
	1: required string Name
}`), From: store.FileChangeTypeDidOpen},
				{URI: "file:///tmp/main.thrift", Version: 0, Content: []byte(`include "gundam.thrift"

struct StrikeRouge {
	1: required Gundam pack
}`), From: store.FileChangeTypeDidOpen},
			},
			pos:       protocol.Position{Line: 0, Character: 7},
			wantLines: []uint32{0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := store.BuildViewForTest(tt.files)

			highlights, err := Highlight(t.Context(), view, tt.files[0].URI, tt.pos)
			require.NoError(t, err)
			require.Len(t, highlights, len(tt.wantLines))

			for i, h := range highlights {
				assert.Equal(t, tt.wantLines[i], h.Range.Start.Line)
				assert.Equal(t, protocol.DocumentHighlightKindText, h.Kind)
				assert.Greater(t, h.Range.End.Character, h.Range.Start.Character,
					"highlight %d covers a range", i)
			}
		})
	}
}

// TestHighlightUnresolvableType pins the minimal result for an identifier
// without references: only the cursor word itself is highlighted.
func TestHighlightUnresolvableType(t *testing.T) {
	view := store.BuildViewForTest([]*store.FileChange{
		{URI: "file:///tmp/main.thrift", Version: 0, Content: []byte("struct Gundam {\n\t1: required UnknownType pack\n}"), From: store.FileChangeTypeDidOpen},
	})

	highlights, err := Highlight(t.Context(), view, "file:///tmp/main.thrift", protocol.Position{Line: 1, Character: 20})
	require.NoError(t, err)
	require.Len(t, highlights, 1)
	assert.Equal(t, protocol.Range{
		Start: protocol.Position{Line: 1, Character: 13},
		End:   protocol.Position{Line: 1, Character: 24},
	}, highlights[0].Range, "cursor word is highlighted exactly")
}
