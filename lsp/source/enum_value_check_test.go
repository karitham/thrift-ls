package source

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
)

func Test_EnumValueCheck_Diagnostic(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []protocol.Diagnostic
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
			want: []protocol.Diagnostic{
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 1, Character: 2},
						End:   protocol.Position{Line: 1, Character: 5},
					},
					Severity: protocol.DiagnosticSeverityWarning,
					Source:   protocol.NewOptional("thrift-ls"),
					Code:     protocol.String(CodeImplicitEnumValue),
					Message:  protocol.String("RED has no explicit value (implicitly 0)"),
				},
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 3, Character: 2},
						End:   protocol.Position{Line: 3, Character: 6},
					},
					Severity: protocol.DiagnosticSeverityWarning,
					Source:   protocol.NewOptional("thrift-ls"),
					Code:     protocol.String(CodeImplicitEnumValue),
					Message:  protocol.String("BLUE has no explicit value (implicitly 3)"),
				},
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 5, Character: 2},
						End:   protocol.Position{Line: 5, Character: 7},
					},
					Severity: protocol.DiagnosticSeverityWarning,
					Source:   protocol.NewOptional("thrift-ls"),
					Code:     protocol.String(CodeImplicitEnumValue),
					Message:  protocol.String("OMEGA has no explicit value (implicitly 17)"),
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
			want: []protocol.Diagnostic{
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 2, Character: 2},
						End:   protocol.Position{Line: 2, Character: 3},
					},
					Severity: protocol.DiagnosticSeverityWarning,
					Source:   protocol.NewOptional("thrift-ls"),
					Code:     protocol.String(CodeImplicitEnumValue),
					Message:  protocol.String("B has no explicit value"),
				},
			},
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

			got, err := (&EnumValueCheck{}).Diagnostic(t.Context(), ss, []uri.URI{"file:///tmp/user.thrift"})
			assert.NoError(t, err)

			assert.Equal(t, DiagnosticResult{"file:///tmp/user.thrift": tt.want}, got)
		})
	}
}
