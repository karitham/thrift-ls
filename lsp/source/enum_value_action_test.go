package source

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/mapper"
)

func Test_MakeEnumValuesExplicitAction(t *testing.T) {
	tests := []struct {
		name    string
		content string
		rng     protocol.Range
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
			rng: pointRange(1, 2),
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
			rng:     pointRange(1, 2),
			want:    "",
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
			rng:  pointRange(1, 5),
			want: "",
		},
		{
			name:    "unparseable precedent is a no-op",
			content: "enum E {\n  A = 08,\n  B\n}\n",
			rng:     pointRange(2, 2),
			want:    "",
		},
		{
			name:    "parse errors are a no-op",
			content: "enum E {\n  A,\n",
			rng:     pointRange(1, 2),
			want:    "",
		},
		{
			name: "only the enum under the cursor is edited",
			content: `enum A {
  X,
}

enum B {
  Y,
}
`,
			rng:  pointRange(5, 2),
			want: "enum A {\n  X,\n}\n\nenum B {\n  Y = 0,\n}\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ss := buildSnapshotForTest(t, []*cache.FileChange{
				{
					URI:     "file:///tmp/user.thrift",
					Version: 0,
					Content: []byte(tt.content),
					From:    cache.FileChangeTypeDidOpen,
				},
			})

			fh, err := ss.ReadFile(t.Context(), "file:///tmp/user.thrift")
			require.NoError(t, err)

			act, err := MakeEnumValuesExplicitAction(t.Context(), ss, fh, tt.rng)
			require.NoError(t, err)

			if tt.want == "" {
				assert.Nil(t, act)
				return
			}

			require.NotNil(t, act)
			assert.Equal(t, "Make enum values explicit", act.Title)
			assert.Equal(t, protocol.CodeActionKindRefactorRewrite, *act.Kind)

			edits := act.Edit.Changes["file:///tmp/user.thrift"]

			got, err := mapper.NewMapper("file:///tmp/user.thrift", []byte(tt.content)).ApplyEdits(edits)
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(got))
		})
	}
}

// pointRange is a collapsed selection: the cursor at line/col, 0-based.
func pointRange(line, character uint32) protocol.Range {
	pos := protocol.Position{Line: line, Character: character}

	return protocol.Range{Start: pos, End: pos}
}
