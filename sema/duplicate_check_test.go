package sema

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/karitham/thrift-ls/lsp/cache"
)

func Test_DuplicateCheck_Diagnostic(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []diagCmp
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
			want: []diagCmp{
				{
					StartLine: 1 + 1, StartCol: 7 + 1,
					EndLine: 1 + 1, EndCol: 8 + 1,
					Severity: SeverityError,
					Code:     CodeDuplicateDef,
					Message:  "duplicate struct A",
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
			want: []diagCmp{
				{
					StartLine: 2 + 1, StartCol: 2 + 1,
					EndLine: 2 + 1, EndCol: 3 + 1,
					Severity: SeverityError,
					Code:     CodeDuplicateDef,
					Message:  "duplicate member A",
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
			want: []diagCmp{
				{
					StartLine: 2 + 1, StartCol: 9 + 1,
					EndLine: 2 + 1, EndCol: 10 + 1,
					Severity: SeverityError,
					Code:     CodeDuplicateDef,
					Message:  "duplicate field a",
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
			want: []diagCmp{
				{
					StartLine: 2 + 1, StartCol: 7 + 1,
					EndLine: 2 + 1, EndCol: 8 + 1,
					Severity: SeverityError,
					Code:     CodeDuplicateDef,
					Message:  "duplicate function f",
				},
			},
		},
		{
			name: "duplicate argument names",
			content: `service S {
  void f(1: i32 x, 2: i32 x),
}
`,
			want: []diagCmp{
				{
					StartLine: 1 + 1, StartCol: 26 + 1,
					EndLine: 1 + 1, EndCol: 27 + 1,
					Severity: SeverityError,
					Code:     CodeDuplicateDef,
					Message:  "duplicate argument x",
				},
			},
		},
		{
			name: "definitions of different kinds share one scope",
			content: `struct User {}
enum User {}
`,
			want: []diagCmp{
				{
					StartLine: 1 + 1, StartCol: 5 + 1,
					EndLine: 1 + 1, EndCol: 9 + 1,
					Severity: SeverityError,
					Code:     CodeDuplicateDef,
					Message:  "duplicate enum User",
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
			want: []diagCmp{
				{
					StartLine: 2 + 1, StartCol: 2 + 1,
					EndLine: 2 + 1, EndCol: 3 + 1,
					Severity: SeverityError,
					Code:     CodeDuplicateEnumVal,
					Message:  "enum value 1 duplicates A",
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
			want: []diagCmp{
				{
					StartLine: 2 + 1, StartCol: 2 + 1,
					EndLine: 2 + 1, EndCol: 3 + 1,
					Severity: SeverityError,
					Code:     CodeDuplicateEnumVal,
					Message:  "enum value 0 duplicates A",
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
			want: []diagCmp{
				{
					StartLine: 2 + 1, StartCol: 2 + 1,
					EndLine: 2 + 1, EndCol: 3 + 1,
					Severity: SeverityError,
					Code:     CodeDuplicateEnumVal,
					Message:  "enum value 0 duplicates A",
				},
				{
					StartLine: 3 + 1, StartCol: 2 + 1,
					EndLine: 3 + 1, EndCol: 3 + 1,
					Severity: SeverityError,
					Code:     CodeDuplicateEnumVal,
					Message:  "enum value 0 duplicates A",
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
			want: []diagCmp{
				{
					StartLine: 2 + 1, StartCol: 2 + 1,
					EndLine: 2 + 1, EndCol: 3 + 1,
					Severity: SeverityError,
					Code:     CodeDuplicateEnumVal,
					Message:  "enum value 16 duplicates A",
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
			want: []diagCmp{
				{
					StartLine: 2 + 1, StartCol: 2 + 1,
					EndLine: 2 + 1, EndCol: 5 + 1,
					Severity: SeverityError,
					Code:     CodeDuplicateValue,
					Message:  `duplicate map key "a"`,
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
			want: []diagCmp{
				{
					StartLine: 2 + 1, StartCol: 2 + 1,
					EndLine: 2 + 1, EndCol: 5 + 1,
					Severity: SeverityError,
					Code:     CodeDuplicateValue,
					Message:  "duplicate map key 0x1",
				},
			},
		},
		{
			name: "duplicate set values",
			content: `const set<i32> S = [1, 2, 1]
`,
			want: []diagCmp{
				{
					StartLine: 0 + 1, StartCol: 26 + 1,
					EndLine: 0 + 1, EndCol: 27 + 1,
					Severity: SeverityError,
					Code:     CodeDuplicateValue,
					Message:  "duplicate set value 1",
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
			want: []diagCmp{
				{
					StartLine: 1 + 1, StartCol: 14 + 1,
					EndLine: 1 + 1, EndCol: 15 + 1,
					Severity: SeverityError,
					Code:     CodeDuplicateValue,
					Message:  "duplicate set value 1",
				},
			},
		},
		{
			name: "field default with duplicate map key",
			content: `struct Foo {
  1: map<string, i32> m = {"a": 1, "a": 2},
}
`,
			want: []diagCmp{
				{
					StartLine: 1 + 1, StartCol: 35 + 1,
					EndLine: 1 + 1, EndCol: 38 + 1,
					Severity: SeverityError,
					Code:     CodeDuplicateValue,
					Message:  `duplicate map key "a"`,
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

			report := runOne(t, EachFile(&DuplicateCheck{}), view, "file:///tmp/user.thrift")

			assert.Equal(t, tt.want, cmpAll(report["file:///tmp/user.thrift"]))
		})
	}
}
