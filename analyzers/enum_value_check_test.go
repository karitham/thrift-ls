package analyzers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karitham/thrift-ls/analyzertest"
	"github.com/karitham/thrift-ls/sema"
)

func Test_EnumValueCheck_Diagnostic(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []analyzertest.Diag
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
			want: []analyzertest.Diag{
				{
					StartLine: 1 + 1, StartCol: 2 + 1,
					EndLine: 1 + 1, EndCol: 5 + 1,
					Severity: sema.SeverityWarning,
					Code:     sema.CodeImplicitEnumValue,
					Message:  "RED has no explicit value (implicitly 0)",
				},
				{
					StartLine: 3 + 1, StartCol: 2 + 1,
					EndLine: 3 + 1, EndCol: 6 + 1,
					Severity: sema.SeverityWarning,
					Code:     sema.CodeImplicitEnumValue,
					Message:  "BLUE has no explicit value (implicitly 3)",
				},
				{
					StartLine: 5 + 1, StartCol: 2 + 1,
					EndLine: 5 + 1, EndCol: 7 + 1,
					Severity: sema.SeverityWarning,
					Code:     sema.CodeImplicitEnumValue,
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
			want: []analyzertest.Diag{
				{
					StartLine: 2 + 1, StartCol: 2 + 1,
					EndLine: 2 + 1, EndCol: 3 + 1,
					Severity: sema.SeverityWarning,
					Code:     sema.CodeImplicitEnumValue,
					Message:  "B has no explicit value",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := analyzertest.Run(t, sema.EachFile(&EnumValueCheck{}), map[string]string{
				"user.thrift": tt.content,
			}, "user.thrift")

			assert.Equal(t, tt.want, analyzertest.Simplify(report[analyzertest.URI("user.thrift")]))
		})
	}
}

// Test_EnumValueCheck_InlineFix pins the inline quickfix: the member's name
// is rewritten to carry the auto-incremented value the compiler resolves
// for it.
func Test_EnumValueCheck_InlineFix(t *testing.T) {
	content := "enum Color {\n  RED,\n  GREEN = 2,\n  BLUE,\n}\n"

	report := analyzertest.Run(t, sema.EachFile(&EnumValueCheck{}), map[string]string{
		"user.thrift": content,
	}, "user.thrift")
	file := analyzertest.URI("user.thrift")
	diags := report[file]
	require.Len(t, diags, 2)

	assert.Equal(t, "Add explicit value 0 to RED", diags[0].Fixes[0].Title)

	out, applied, _, err := sema.Apply([]byte(content), diags[0].Fixes)
	require.NoError(t, err)
	require.Len(t, applied, 1)
	assert.Equal(t, "enum Color {\n  RED = 0,\n  GREEN = 2,\n  BLUE,\n}\n", string(out))

	assert.Equal(t, "Add explicit value 3 to BLUE", diags[1].Fixes[0].Title)

	out, applied, _, err = sema.Apply([]byte(content), diags[1].Fixes)
	require.NoError(t, err)
	require.Len(t, applied, 1)
	assert.Equal(t, "enum Color {\n  RED,\n  GREEN = 2,\n  BLUE = 3,\n}\n", string(out))
}

// Test_EnumValueCheck_UnknownValueNoFix pins the Known=false boundary: a
// member whose implicit value cannot be computed (it follows an
// unparseable constant) offers no fix — writing one would put a wrong
// value in the source. A fixable sibling still gets its fix.
func Test_EnumValueCheck_UnknownValueNoFix(t *testing.T) {
	content := "enum E {\n  A = 08,\n  B,\n  C,\n}\n"

	report := analyzertest.Run(t, sema.EachFile(&EnumValueCheck{}), map[string]string{
		"user.thrift": content,
	}, "user.thrift")
	diags := report[analyzertest.URI("user.thrift")]
	require.Len(t, diags, 2)

	// B follows the broken constant: no computable value, no fix.
	assert.Equal(t, "B has no explicit value", diags[0].Message)
	assert.Empty(t, diags[0].Fixes)

	// C's value is settled by the broken constant's absence: the chain
	// stays unknown, so no fix here either.
	assert.Equal(t, "C has no explicit value", diags[1].Message)
	assert.Empty(t, diags[1].Fixes)
}
