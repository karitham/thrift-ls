package source

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
)

// foldCase is one folding range expectation: the range of lines it covers.
type foldCase struct {
	startLine uint32
	endLine   uint32
	kind      protocol.FoldingRangeKind
}

func foldingRanges(t *testing.T, src string) []protocol.FoldingRange {
	t.Helper()

	dir := t.TempDir()
	file := uri.File(filepath.Join(dir, "test.thrift"))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.thrift"), []byte(src), 0o644))

	view := cache.NewView(uri.File(dir), cache.NewOverlayFS(cache.NewMemoizedFS()), nil)
	view.Update(t.Context(), &cache.FileChange{
		URI:     file,
		Version: 0,
		Content: []byte(src),
		From:    cache.FileChangeTypeInitialize,
	})

	return Ranges(t.Context(), view, file)
}

func TestFoldingRanges(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []foldCase
	}{
		{
			name: "empty file",
			src:  "",
			want: []foldCase{},
		},
		{
			name: "struct body",
			src:  "struct S {\n  1: i32 a\n}",
			want: []foldCase{{0, 2, ""}},
		},
		{
			name: "struct on one line folds nothing",
			src:  "struct S { 1: i32 a }",
			want: []foldCase{},
		},
		{
			name: "enum and service bodies",
			src:  "enum E {\n  A,\n}\n\nservice S {\n  void f(),\n}",
			want: []foldCase{{0, 2, ""}, {4, 6, ""}},
		},
		{
			name: "union and exception bodies",
			src:  "union U {\n  1: i32 a,\n}\n\nexception X {\n  1: string m,\n}",
			want: []foldCase{{0, 2, ""}, {4, 6, ""}},
		},
		{
			name: "const list and map values",
			src:  "const list<i32> l = [\n  1,\n  2,\n]\nconst map<string, i32> m = {\n  \"a\": 1,\n}",
			want: []foldCase{{0, 3, ""}, {4, 6, ""}},
		},
		{
			name: "annotations fold",
			src:  "struct S {\n}\n(\n  a = \"1\",\n)",
			want: []foldCase{{0, 1, ""}, {2, 4, ""}},
		},
		{
			name: "comment block folds",
			src:  "// one\n// two\n// three\nstruct S {}",
			want: []foldCase{{0, 2, protocol.FoldingRangeKindComment}},
		},
		{
			name: "comment run broken by a blank line does not fold",
			src:  "// one\n\n// two\nstruct S {}",
			want: []foldCase{},
		},
		{
			name: "multi-line doc comment folds",
			src:  "/**\n * doc\n */\nstruct S {}",
			want: []foldCase{{0, 2, protocol.FoldingRangeKindComment}},
		},
		{
			name: "annotation lines fold as comments",
			src:  "@deprecation.Deprecated{}\n@naming.X{'a': 'b'}\nstruct S {}",
			want: []foldCase{{0, 1, protocol.FoldingRangeKindComment}},
		},
		{
			name: "mixed bodies, values, and comments",
			src: `// header
// comments
struct S {
  1: i32 a,
}

const list<i32> l = [
  1,
]`,
			want: []foldCase{
				{0, 1, protocol.FoldingRangeKindComment},
				{2, 4, ""},
				{6, 8, ""},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ranges := foldingRanges(t, tt.src)

			got := make([]foldCase, len(ranges))
			for i, r := range ranges {
				kind := r.Kind
				got[i] = foldCase{r.StartLine, r.EndLine, kind}
			}

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFoldingRangesPositions(t *testing.T) {
	src := "struct S {\n  1: i32 a\n}"

	ranges := foldingRanges(t, src)
	require.Len(t, ranges, 1)

	// The range spans from the opening brace to the closing one.
	require.NotNil(t, ranges[0].StartCharacter)
	require.NotNil(t, ranges[0].EndCharacter)
	assert.Equal(t, uint32(9), *ranges[0].StartCharacter)
	assert.Equal(t, uint32(1), *ranges[0].EndCharacter)
}

// TestFoldingRangesParseErrors ensures a broken document yields no ranges
// instead of panicking.
func TestFoldingRangesParseErrors(t *testing.T) {
	assert.NotPanics(t, func() {
		_ = foldingRanges(t, "struct S {")
	})
}

var _ = context.Background
