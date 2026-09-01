package sema

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/karitham/thrift-ls/lsp/cache"
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
			view := buildSnapshotForTest(t, []*cache.FileChange{
				{
					URI:     "file:///tmp/user.thrift",
					Version: 0,
					Content: []byte(tt.content),
					From:    cache.FileChangeTypeDidOpen,
				},
			})

			report := runOne(t, EachFile(&EnumValueCheck{}), view, "file:///tmp/user.thrift")

			assert.Equal(t, tt.want, cmpAll(report["file:///tmp/user.thrift"]))
		})
	}
}
