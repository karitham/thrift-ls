package sema

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/cache"
)

// applyEdits applies edits to content by byte offset, last-first. The
// test sources are ASCII, so byte offsets are unambiguous.
func applyEdits(t *testing.T, content string, edits []Edit) string {
	t.Helper()

	sorted := append([]Edit(nil), edits...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Span.Start.Offset > sorted[j].Span.Start.Offset
	})

	out := []byte(content)
	for _, e := range sorted {
		out = append(out[:e.Span.Start.Offset], append([]byte(e.NewText), out[e.Span.End.Offset:]...)...)
	}

	return string(out)
}

// spanAt returns a one-point selection at a 1-based parser line/column,
// with real offsets resolved through the file's mapper.
func spanAt(t *testing.T, pf *cache.ParsedFile, line, col int) Span {
	t.Helper()

	p, err := pf.Mapper().LSPPosToParserPosition(protocol.Position{Line: uint32(line - 1), Character: uint32(col - 1)})
	require.NoError(t, err)

	return Span{Start: p, End: p}
}

func Test_EnumValuesProvider(t *testing.T) {
	tests := []struct {
		name    string
		content string
		line    int // the selection's 1-based line
		col     int
		want    string // resulting content; empty means no action
	}{
		{
			name: "fills implicit member values",
			content: `enum Color {
  RED,
  GREEN = 2,
  BLUE,
  ALPHA = 0x10,
  OMEGA,
}
`,
			line: 2, col: 3,
			want: `enum Color {
  RED = 0,
  GREEN = 2,
  BLUE = 3,
  ALPHA = 0x10,
  OMEGA = 17,
}
`,
		},
		{
			name:    "fully explicit enum is a no-op",
			content: "enum E {\n  A = 1,\n  B = 2\n}\n",
			line:    2, col: 3,
			want: "",
		},
		{
			name: "selection outside every enum is a no-op",
			content: `struct S {
  1: i32 a,
}

enum E {
  A,
}
`,
			line: 2, col: 6,
			want: "",
		},
		{
			name:    "unparseable precedent is a no-op",
			content: "enum E {\n  A = 08,\n  B\n}\n",
			line:    3, col: 3,
			want: "",
		},
		{
			name:    "parse errors are a no-op",
			content: "enum E {\n  A,\n",
			line:    2, col: 3,
			want: "",
		},
		{
			name:    "only the enum under the cursor is edited",
			content: "enum A {\n  X,\n}\n\nenum B {\n  Y,\n}\n",
			line:    6, col: 3,
			want: "enum A {\n  X,\n}\n\nenum B {\n  Y = 0,\n}\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := buildSnapshotForTest(t, []*cache.FileChange{
				{
					URI:     "file:///tmp/user.thrift",
					Version: 0,
					Content: []byte(tt.content),
					From:    cache.FileChangeTypeDidOpen,
				},
			})

			pf, err := view.Parse(t.Context(), "file:///tmp/user.thrift")
			require.NoError(t, err)

			actions := EnumValuesProvider{}.Actions(t.Context(),
				File{URI: "file:///tmp/user.thrift", PF: pf}, spanAt(t, pf, tt.line, tt.col), Report{})

			if tt.want == "" {
				assert.Empty(t, actions)
				return
			}

			require.Len(t, actions, 1)
			assert.Equal(t, "Make enum values explicit", actions[0].Title)
			assert.Equal(t, tt.want, applyEdits(t, tt.content, actions[0].Edits))
		})
	}
}

// Test_EnumValuesProvider_QuickFixPromotion pins the promotion: a
// diagnostic overlapping the selection offers the rewrite also as its
// quickfix.
func Test_EnumValuesProvider_QuickFixPromotion(t *testing.T) {
	content := "enum E { A, B = 1 }\n"

	view := buildSnapshotForTest(t, []*cache.FileChange{
		{
			URI:     "file:///tmp/user.thrift",
			Version: 0,
			Content: []byte(content),
			From:    cache.FileChangeTypeDidOpen,
		},
	})

	pf, err := view.Parse(t.Context(), "file:///tmp/user.thrift")
	require.NoError(t, err)

	f := File{URI: "file:///tmp/user.thrift", PF: pf}

	report := runOne(t, EachFile(&EnumValueCheck{}), view, f.URI)

	t.Run("a diagnostic on the selection promotes the action", func(t *testing.T) {
		actions := EnumValuesProvider{}.Actions(t.Context(), f, spanAt(t, pf, 1, 10), report)
		require.NotEmpty(t, actions)

		fixes := 0
		for _, a := range actions {
			if a.Fix {
				fixes++
			}
		}

		assert.Equal(t, 1, fixes)
	})

	t.Run("a clean selection offers the rewrite only", func(t *testing.T) {
		actions := EnumValuesProvider{}.Actions(t.Context(), f, spanAt(t, pf, 1, 18), report)
		require.Len(t, actions, 1)
		assert.False(t, actions[0].Fix)
	})
}

func Test_FieldQualifierProvider(t *testing.T) {
	content := "struct S {\n  1: i32 a,\n  2: required string b,\n  3: optional i64 c,\n}\n"

	view := buildSnapshotForTest(t, []*cache.FileChange{
		{
			URI:     "file:///tmp/user.thrift",
			Version: 0,
			Content: []byte(content),
			From:    cache.FileChangeTypeDidOpen,
		},
	})

	pf, err := view.Parse(t.Context(), "file:///tmp/user.thrift")
	require.NoError(t, err)

	// Selection covers the whole struct: every field yields the
	// qualifiers it does not already carry.
	selection := Span{Start: spanAt(t, pf, 1, 1).Start, End: spanAt(t, pf, 5, 2).Start}
	actions := FieldQualifierProvider{}.Actions(t.Context(),
		File{URI: "file:///tmp/user.thrift", PF: pf}, selection, Report{})

	got := make([]string, 0, len(actions))
	for _, a := range actions {
		got = append(got, a.Title)
	}

	assert.Equal(t, []string{
		"Make field required (a)",
		"Make field optional (a)",
		"Make field optional (b)",
		"Make field required (c)",
	}, got)

	// Applying the first edit inserts the qualifier keyword.
	assert.Equal(t, "struct S {\n  1: required i32 a,\n  2: required string b,\n  3: optional i64 c,\n}\n",
		applyEdits(t, content, actions[0].Edits))
}
