package links

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
)

// buildSnapshot parses src as the file at URI and returns the snapshot.
func buildSnapshot(t *testing.T, file uri.URI, src string) *cache.Snapshot {
	t.Helper()

	ss := cache.BuildSnapshotForTest([]*cache.FileChange{
		{URI: file, Version: 0, Content: []byte(src), From: cache.FileChangeTypeDidOpen},
	})

	return ss
}

func TestLinks(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []struct {
			line   uint32
			target uri.URI
		}
	}{
		{
			name: "include and cpp_include links",
			src: `include "base.thrift"
cpp_include "types.h"

struct S {}`,
			want: []struct {
				line   uint32
				target uri.URI
			}{
				{0, "file:///tmp/base.thrift"},
				{1, "file:///tmp/types.h"},
			},
		},
		{
			name: "no includes yields no links",
			src:  "struct S {}",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ss := buildSnapshot(t, "file:///tmp/main.thrift", tt.src)

			got := Links(t.Context(), ss, "file:///tmp/main.thrift")

			if tt.want == nil {
				assert.Empty(t, got)

				return
			}

			require.Len(t, got, len(tt.want))

			for i, want := range tt.want {
				assert.Equal(t, want.line, got[i].Range.Start.Line)
				require.NotNil(t, got[i].Target)
				assert.Equal(t, want.target, *got[i].Target)
			}
		})
	}
}

// TestLinksRange pins the link range to the include string literal.
func TestLinksRange(t *testing.T) {
	ss := buildSnapshot(t, "file:///tmp/main.thrift", "include \"base.thrift\"\n")

	got := Links(t.Context(), ss, "file:///tmp/main.thrift")
	require.Len(t, got, 1)

	assert.Equal(t, uint32(0), got[0].Range.Start.Line)
	assert.Equal(t, uint32(8), got[0].Range.Start.Character)
	assert.Equal(t, uint32(0), got[0].Range.End.Line)
	assert.Equal(t, uint32(21), got[0].Range.End.Character)
}
