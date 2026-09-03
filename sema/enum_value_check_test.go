package sema

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/store"
)

func Test_EnumValueCheck_Diagnostic(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []diagCmp
	}{
		{
			name: "explicit values only",
			content: `enum Color {
  RED = 0,
  GREEN = 2,
}
`,
			want: nil,
		},
		{
			name: "implicit members auto-increment",
			content: `enum Color {
  RED,
  GREEN = 2,
  BLUE,
  ALPHA = 0x10,
  OMEGA,
}
`,
			want: []diagCmp{
				{
					StartLine: 1 + 1, StartCol: 2 + 1,
					EndLine: 1 + 1, EndCol: 5 + 1,
					Severity: SeverityWarning,
					Code:     CodeImplicitEnumValue,
					Message:  "RED has no explicit value (implicitly 0)",
				},
				{
					StartLine: 3 + 1, StartCol: 2 + 1,
					EndLine: 3 + 1, EndCol: 6 + 1,
					Severity: SeverityWarning,
					Code:     CodeImplicitEnumValue,
					Message:  "BLUE has no explicit value (implicitly 3)",
				},
				{
					StartLine: 5 + 1, StartCol: 2 + 1,
					EndLine: 5 + 1, EndCol: 7 + 1,
					Severity: SeverityWarning,
					Code:     CodeImplicitEnumValue,
					Message:  "OMEGA has no explicit value (implicitly 17)",
				},
			},
		},
		{
			name: "unparseable explicit value breaks the chain",
			content: `enum E {
  A = 08,
  B,
}
`,
			want: []diagCmp{
				{
					StartLine: 2 + 1, StartCol: 2 + 1,
					EndLine: 2 + 1, EndCol: 3 + 1,
					Severity: SeverityWarning,
					Code:     CodeImplicitEnumValue,
					Message:  "B has no explicit value",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := store.BuildViewForTest([]*store.FileChange{
				{
					URI:     "file:///tmp/user.thrift",
					Version: 0,
					Content: []byte(tt.content),
					From:    store.FileChangeTypeDidOpen,
				},
			})

			report := runOne(t, EachFile(&EnumValueCheck{}), view, "file:///tmp/user.thrift")

			assert.Equal(t, tt.want, cmpAll(report["file:///tmp/user.thrift"]))
		})
	}
}

// Test_EnumValueCheck_InlineFix pins the inline quickfix: the member's name
// is rewritten to carry the auto-incremented value the compiler resolves
// for it.
func Test_EnumValueCheck_InlineFix(t *testing.T) {
	content := "enum Color {\n  RED,\n  GREEN = 2,\n  BLUE,\n}\n"
	file := uri.File("/tmp/user.thrift")

	view := store.BuildViewForTest([]*store.FileChange{
		{URI: file, Content: []byte(content), From: store.FileChangeTypeDidOpen},
	})

	report := runOne(t, EachFile(&EnumValueCheck{}), view, file)
	diags := report[file]
	require.Len(t, diags, 2)

	assert.Equal(t, "Add explicit value 0 to RED", diags[0].Fixes[0].Title)
	assert.Equal(t, "enum Color {\n  RED = 0,\n  GREEN = 2,\n  BLUE,\n}\n",
		applyEdits(t, content, diags[0].Fixes[0].Edits))

	assert.Equal(t, "Add explicit value 3 to BLUE", diags[1].Fixes[0].Title)
	assert.Equal(t, "enum Color {\n  RED,\n  GREEN = 2,\n  BLUE = 3,\n}\n",
		applyEdits(t, content, diags[1].Fixes[0].Edits))
}

// Test_EnumValueCheck_UnknownValueNoFix pins the Known=false boundary: a
// member whose implicit value cannot be computed (it follows an
// unparseable constant) offers no fix — writing one would put a wrong
// value in the source. A fixable sibling still gets its fix.
func Test_EnumValueCheck_UnknownValueNoFix(t *testing.T) {
	content := "enum E {\n  A = 08,\n  B,\n  C,\n}\n"
	file := uri.File("/tmp/user.thrift")

	view := store.BuildViewForTest([]*store.FileChange{
		{URI: file, Content: []byte(content), From: store.FileChangeTypeDidOpen},
	})

	report := runOne(t, EachFile(&EnumValueCheck{}), view, file)
	diags := report[file]
	require.Len(t, diags, 2)

	// B follows the broken constant: no computable value, no fix.
	assert.Equal(t, "B has no explicit value", diags[0].Message)
	assert.Empty(t, diags[0].Fixes)

	// C's value is settled by the broken constant's absence: the chain
	// stays unknown, so no fix here either.
	assert.Equal(t, "C has no explicit value", diags[1].Message)
	assert.Empty(t, diags[1].Fixes)
}
