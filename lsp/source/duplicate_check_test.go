package source

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
)

func Test_DuplicateCheck_Diagnostic(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []protocol.Diagnostic
	}{
		{
			name: "distinct names and values are clean",
			content: `struct User {}
enum Color {
  RED = 0,
  GREEN = 2,
}
service S {
  void f(1: i32 x),
}
`,
			want: nil,
		},
		{
			name: "duplicate top-level definitions",
			content: `struct A {}
struct A {}
`,
			want: []protocol.Diagnostic{
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 1, Character: 7},
						End:   protocol.Position{Line: 1, Character: 8},
					},
					Severity: protocol.DiagnosticSeverityError,
					Source:   protocol.NewOptional("thrift-ls"),
					Code:     protocol.String(CodeDuplicateDef),
					Message:  protocol.String("duplicate struct A"),
				},
			},
		},
		{
			name: "duplicate enum member names",
			content: `enum E {
  A,
  A,
}
`,
			want: []protocol.Diagnostic{
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 2, Character: 2},
						End:   protocol.Position{Line: 2, Character: 3},
					},
					Severity: protocol.DiagnosticSeverityError,
					Source:   protocol.NewOptional("thrift-ls"),
					Code:     protocol.String(CodeDuplicateDef),
					Message:  protocol.String("duplicate member A"),
				},
			},
		},
		{
			name: "duplicate field names",
			content: `struct S {
  1: i32 a,
  2: i32 a,
}
`,
			want: []protocol.Diagnostic{
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 2, Character: 9},
						End:   protocol.Position{Line: 2, Character: 10},
					},
					Severity: protocol.DiagnosticSeverityError,
					Source:   protocol.NewOptional("thrift-ls"),
					Code:     protocol.String(CodeDuplicateDef),
					Message:  protocol.String("duplicate field a"),
				},
			},
		},
		{
			name: "duplicate function names",
			content: `service S {
  void f(),
  void f(),
}
`,
			want: []protocol.Diagnostic{
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 2, Character: 7},
						End:   protocol.Position{Line: 2, Character: 8},
					},
					Severity: protocol.DiagnosticSeverityError,
					Source:   protocol.NewOptional("thrift-ls"),
					Code:     protocol.String(CodeDuplicateDef),
					Message:  protocol.String("duplicate function f"),
				},
			},
		},
		{
			name: "duplicate argument names",
			content: `service S {
  void f(1: i32 x, 2: i32 x),
}
`,
			want: []protocol.Diagnostic{
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 1, Character: 26},
						End:   protocol.Position{Line: 1, Character: 27},
					},
					Severity: protocol.DiagnosticSeverityError,
					Source:   protocol.NewOptional("thrift-ls"),
					Code:     protocol.String(CodeDuplicateDef),
					Message:  protocol.String("duplicate argument x"),
				},
			},
		},
		{
			name: "definitions of different kinds share one scope",
			content: `struct User {}
enum User {}
`,
			want: []protocol.Diagnostic{
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 1, Character: 5},
						End:   protocol.Position{Line: 1, Character: 9},
					},
					Severity: protocol.DiagnosticSeverityError,
					Source:   protocol.NewOptional("thrift-ls"),
					Code:     protocol.String(CodeDuplicateDef),
					Message:  protocol.String("duplicate enum User"),
				},
			},
		},
		{
			name: "explicit duplicate enum values",
			content: `enum E {
  A = 1,
  B = 1,
}
`,
			want: []protocol.Diagnostic{
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 2, Character: 2},
						End:   protocol.Position{Line: 2, Character: 3},
					},
					Severity: protocol.DiagnosticSeverityError,
					Source:   protocol.NewOptional("thrift-ls"),
					Code:     protocol.String(CodeDuplicateEnumVal),
					Message:  protocol.String("enum value 1 duplicates A"),
				},
			},
		},
		{
			name: "implicit member collides with explicit value",
			content: `enum E {
  A,
  B = 0,
}
`,
			want: []protocol.Diagnostic{
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 2, Character: 2},
						End:   protocol.Position{Line: 2, Character: 3},
					},
					Severity: protocol.DiagnosticSeverityError,
					Source:   protocol.NewOptional("thrift-ls"),
					Code:     protocol.String(CodeDuplicateEnumVal),
					Message:  protocol.String("enum value 0 duplicates A"),
				},
			},
		},
		{
			name: "unparseable constant skips the value chain",
			content: `enum E {
  A = 08,
  B,
  C = 0,
}
`,
			want: nil,
		},
		{
			name: "multiple duplicate enum values",
			content: `enum E {
  A = 0,
  B = 0,
  C = 0,
}
`,
			want: []protocol.Diagnostic{
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 2, Character: 2},
						End:   protocol.Position{Line: 2, Character: 3},
					},
					Severity: protocol.DiagnosticSeverityError,
					Source:   protocol.NewOptional("thrift-ls"),
					Code:     protocol.String(CodeDuplicateEnumVal),
					Message:  protocol.String("enum value 0 duplicates A"),
				},
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 3, Character: 2},
						End:   protocol.Position{Line: 3, Character: 3},
					},
					Severity: protocol.DiagnosticSeverityError,
					Source:   protocol.NewOptional("thrift-ls"),
					Code:     protocol.String(CodeDuplicateEnumVal),
					Message:  protocol.String("enum value 0 duplicates A"),
				},
			},
		},
		{
			name: "hex and decimal enum values collide",
			content: `enum E {
  A = 0x10,
  B = 16,
}
`,
			want: []protocol.Diagnostic{
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 2, Character: 2},
						End:   protocol.Position{Line: 2, Character: 3},
					},
					Severity: protocol.DiagnosticSeverityError,
					Source:   protocol.NewOptional("thrift-ls"),
					Code:     protocol.String(CodeDuplicateEnumVal),
					Message:  protocol.String("enum value 16 duplicates A"),
				},
			},
		},
		{
			name: "duplicate map string keys",
			content: `const map<string, i32> M = {
  "a": 1,
  "a": 2,
}
`,
			want: []protocol.Diagnostic{
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 2, Character: 2},
						End:   protocol.Position{Line: 2, Character: 5},
					},
					Severity: protocol.DiagnosticSeverityError,
					Source:   protocol.NewOptional("thrift-ls"),
					Code:     protocol.String(CodeDuplicateValue),
					Message:  protocol.String(`duplicate map key "a"`),
				},
			},
		},
		{
			name: "map keys collide by numeric value",
			content: `const map<i32, i32> M = {
  1: 1,
  0x1: 2,
}
`,
			want: []protocol.Diagnostic{
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 2, Character: 2},
						End:   protocol.Position{Line: 2, Character: 5},
					},
					Severity: protocol.DiagnosticSeverityError,
					Source:   protocol.NewOptional("thrift-ls"),
					Code:     protocol.String(CodeDuplicateValue),
					Message:  protocol.String("duplicate map key 0x1"),
				},
			},
		},
		{
			name: "duplicate set values",
			content: `const set<i32> S = [1, 2, 1]
`,
			want: []protocol.Diagnostic{
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 0, Character: 26},
						End:   protocol.Position{Line: 0, Character: 27},
					},
					Severity: protocol.DiagnosticSeverityError,
					Source:   protocol.NewOptional("thrift-ls"),
					Code:     protocol.String(CodeDuplicateValue),
					Message:  protocol.String("duplicate set value 1"),
				},
			},
		},
		{
			name: "list keeps duplicate values",
			content: `const list<i32> L = [1, 1]
`,
			want: nil,
		},
		{
			name: "nested set in map value",
			content: `const map<string, set<i32>> M = {
  "a": [1, 2, 1],
}
`,
			want: []protocol.Diagnostic{
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 1, Character: 14},
						End:   protocol.Position{Line: 1, Character: 15},
					},
					Severity: protocol.DiagnosticSeverityError,
					Source:   protocol.NewOptional("thrift-ls"),
					Code:     protocol.String(CodeDuplicateValue),
					Message:  protocol.String("duplicate set value 1"),
				},
			},
		},
		{
			name: "field default with duplicate map key",
			content: `struct Foo {
  1: map<string, i32> m = {"a": 1, "a": 2},
}
`,
			want: []protocol.Diagnostic{
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 1, Character: 35},
						End:   protocol.Position{Line: 1, Character: 38},
					},
					Severity: protocol.DiagnosticSeverityError,
					Source:   protocol.NewOptional("thrift-ls"),
					Code:     protocol.String(CodeDuplicateValue),
					Message:  protocol.String(`duplicate map key "a"`),
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

			got, err := (&DuplicateCheck{}).Diagnostic(t.Context(), ss, []uri.URI{"file:///tmp/user.thrift"})
			assert.NoError(t, err)

			assert.Equal(t, DiagnosticResult{"file:///tmp/user.thrift": tt.want}, got)
		})
	}
}
