package codejump

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/cache"
)

// TestHighlightSameFile pins document highlighting: the identifier at the
// cursor highlights its same-file references only.
func TestHighlightSameFile(t *testing.T) {
	tests := []struct {
		name      string
		files     []*cache.FileChange
		pos       protocol.Position // cursor position in the first file
		wantLines []uint32          // highlighted lines in the first file
	}{
		{
			name: "type name highlights definition and usages",
			files: []*cache.FileChange{
				{URI: "file:///tmp/main.thrift", Version: 0, Content: []byte(`struct Gundam {
	1: required string Name
}

struct StrikeRouge {
	1: required Gundam pack
	2: optional Gundam beamSaber
}`), From: cache.FileChangeTypeDidOpen},
			},
			pos:       protocol.Position{Line: 0, Character: 7},
			wantLines: []uint32{0, 5, 6},
		},
		{
			name: "usage highlights the definition too",
			files: []*cache.FileChange{
				{URI: "file:///tmp/main.thrift", Version: 0, Content: []byte(`struct Gundam {
	1: required string Name
}

struct StrikeRouge {
	1: required Gundam pack
}`), From: cache.FileChangeTypeDidOpen},
			},
			pos:       protocol.Position{Line: 5, Character: 14},
			wantLines: []uint32{0, 5},
		},
		{
			name: "cross-file references are excluded",
			files: []*cache.FileChange{
				{URI: "file:///tmp/gundam.thrift", Version: 0, Content: []byte(`struct Gundam {
	1: required string Name
}`), From: cache.FileChangeTypeDidOpen},
				{URI: "file:///tmp/main.thrift", Version: 0, Content: []byte(`include "gundam.thrift"

struct StrikeRouge {
	1: required Gundam pack
}`), From: cache.FileChangeTypeDidOpen},
			},
			pos:       protocol.Position{Line: 0, Character: 7},
			wantLines: []uint32{0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ss := cache.BuildSnapshotForTest(tt.files)

			highlights, err := Highlight(t.Context(), ss, tt.files[0].URI, tt.pos)
			require.NoError(t, err)

			lines := make([]uint32, len(highlights))
			for i, h := range highlights {
				lines[i] = h.Range.Start.Line
				assert.Equal(t, protocol.DocumentHighlightKindText, h.Kind)
			}

			assert.Equal(t, tt.wantLines, lines)
		})
	}
}

// TestHighlightUnresolvableType pins the minimal result for an identifier
// without references: only the cursor word itself is highlighted.
func TestHighlightUnresolvableType(t *testing.T) {
	ss := cache.BuildSnapshotForTest([]*cache.FileChange{
		{URI: "file:///tmp/main.thrift", Version: 0, Content: []byte("struct Gundam {\n\t1: required UnknownType pack\n}"), From: cache.FileChangeTypeDidOpen},
	})

	highlights, err := Highlight(t.Context(), ss, "file:///tmp/main.thrift", protocol.Position{Line: 1, Character: 20})
	require.NoError(t, err)
	require.Len(t, highlights, 1)
	assert.Equal(t, uint32(1), highlights[0].Range.Start.Line)
}
