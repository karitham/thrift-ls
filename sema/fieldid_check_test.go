package sema

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
)

func Test_FieldIDCheck_Diagnostic(t *testing.T) {
	file1 := `struct Test {
  1: required string name,
  1: required string email,
  0: required string test1,
  32768: required int test2,
}

union Test2 {
  1: required string name,
  1: required string email,
  0: required string test1,
  32768: required int test2,
}

exception Test3 {
  1: required string name,
  1: required string email,
  0: required string test1,
  32768: required int test2,
} // line 20

service Demo {
  Test Api1(0:Test arg, 1: Test2 arg1, 1: Test2 arg2, 32768: int arg4),
  Test Api2(1: Test2 arg1) throws (0:Test3 err, 1:Test3 err1, 1:Test3 err2, 32768:Test3 err4)
}
`

	view := buildSnapshotForTest(t, []*cache.FileChange{
		{
			URI:     "file:///tmp/user.thrift",
			Version: 0,
			Content: []byte(file1),
			From:    cache.FileChangeTypeDidOpen,
		},
	})

	type args struct {
		ctx         context.Context
		view        *cache.View
		changeFiles []uri.URI
	}

	tests := []struct {
		name      string
		c         *FieldIDCheck
		args      args
		want      map[uri.URI][]diagCmp
		assertion assert.ErrorAssertionFunc
	}{
		{
			name: "case1",
			c:    &FieldIDCheck{},
			args: args{
				ctx:  t.Context(),
				view: view,
				changeFiles: []uri.URI{
					"file:///tmp/user.thrift",
				},
			},
			want: map[uri.URI][]diagCmp{
				"file:///tmp/user.thrift": {
					// struct
					{
						StartLine: 1 + 1, StartCol: 2 + 1, EndLine: 1 + 1, EndCol: 3 + 1,
						Severity: SeverityError,
						Code:     CodeFieldIDConflict,
						Message:  "field id conflict",
					},
					{
						StartLine: 2 + 1, StartCol: 2 + 1, EndLine: 2 + 1, EndCol: 3 + 1,
						Severity: SeverityError,
						Code:     CodeFieldIDConflict,
						Message:  "field id conflict",
					},
					{
						StartLine: 3 + 1, StartCol: 2 + 1, EndLine: 3 + 1, EndCol: 3 + 1,
						Severity: SeverityError,
						Code:     CodeFieldIDRange,
						Message:  "field id should be a positive integer in [1, 32767]",
					},
					{
						StartLine: 4 + 1, StartCol: 2 + 1, EndLine: 4 + 1, EndCol: 7 + 1,
						Severity: SeverityError,
						Code:     CodeFieldIDRange,
						Message:  "field id should be a positive integer in [1, 32767]",
					},

					// union
					{
						StartLine: 8 + 1, StartCol: 2 + 1, EndLine: 8 + 1, EndCol: 3 + 1,
						Severity: SeverityError,
						Code:     CodeFieldIDConflict,
						Message:  "field id conflict",
					},
					{
						StartLine: 9 + 1, StartCol: 2 + 1, EndLine: 9 + 1, EndCol: 3 + 1,
						Severity: SeverityError,
						Code:     CodeFieldIDConflict,
						Message:  "field id conflict",
					},
					{
						StartLine: 10 + 1, StartCol: 2 + 1, EndLine: 10 + 1, EndCol: 3 + 1,
						Severity: SeverityError,
						Code:     CodeFieldIDRange,
						Message:  "field id should be a positive integer in [1, 32767]",
					},
					{
						StartLine: 11 + 1, StartCol: 2 + 1, EndLine: 11 + 1, EndCol: 7 + 1,
						Severity: SeverityError,
						Code:     CodeFieldIDRange,
						Message:  "field id should be a positive integer in [1, 32767]",
					},

					// exception
					{
						StartLine: 15 + 1, StartCol: 2 + 1, EndLine: 15 + 1, EndCol: 3 + 1,
						Severity: SeverityError,
						Code:     CodeFieldIDConflict,
						Message:  "field id conflict",
					},
					{
						StartLine: 16 + 1, StartCol: 2 + 1, EndLine: 16 + 1, EndCol: 3 + 1,
						Severity: SeverityError,
						Code:     CodeFieldIDConflict,
						Message:  "field id conflict",
					},
					{
						StartLine: 17 + 1, StartCol: 2 + 1, EndLine: 17 + 1, EndCol: 3 + 1,
						Severity: SeverityError,
						Code:     CodeFieldIDRange,
						Message:  "field id should be a positive integer in [1, 32767]",
					},
					{
						StartLine: 18 + 1, StartCol: 2 + 1, EndLine: 18 + 1, EndCol: 7 + 1,
						Severity: SeverityError,
						Code:     CodeFieldIDRange,
						Message:  "field id should be a positive integer in [1, 32767]",
					},

					// function params
					{
						StartLine: 22 + 1, StartCol: 12 + 1, EndLine: 22 + 1, EndCol: 13 + 1,
						Severity: SeverityError,
						Code:     CodeFieldIDRange,
						Message:  "field id should be a positive integer in [1, 32767]",
					},
					{
						StartLine: 22 + 1, StartCol: 24 + 1, EndLine: 22 + 1, EndCol: 25 + 1,
						Severity: SeverityError,
						Code:     CodeFieldIDConflict,
						Message:  "field id conflict",
					},
					{
						StartLine: 22 + 1, StartCol: 39 + 1, EndLine: 22 + 1, EndCol: 40 + 1,
						Severity: SeverityError,
						Code:     CodeFieldIDConflict,
						Message:  "field id conflict",
					},
					{
						StartLine: 22 + 1, StartCol: 54 + 1, EndLine: 22 + 1, EndCol: 59 + 1,
						Severity: SeverityError,
						Code:     CodeFieldIDRange,
						Message:  "field id should be a positive integer in [1, 32767]",
					},

					// function throws
					{
						StartLine: 23 + 1, StartCol: 35 + 1, EndLine: 23 + 1, EndCol: 36 + 1,
						Severity: SeverityError,
						Code:     CodeFieldIDRange,
						Message:  "field id should be a positive integer in [1, 32767]",
					},
					{
						StartLine: 23 + 1, StartCol: 48 + 1, EndLine: 23 + 1, EndCol: 49 + 1,
						Severity: SeverityError,
						Code:     CodeFieldIDConflict,
						Message:  "field id conflict",
					},
					{
						StartLine: 23 + 1, StartCol: 62 + 1, EndLine: 23 + 1, EndCol: 63 + 1,
						Severity: SeverityError,
						Code:     CodeFieldIDConflict,
						Message:  "field id conflict",
					},
					{
						StartLine: 23 + 1, StartCol: 76 + 1, EndLine: 23 + 1, EndCol: 81 + 1,
						Severity: SeverityError,
						Code:     CodeFieldIDRange,
						Message:  "field id should be a positive integer in [1, 32767]",
					},
				},
			},
			assertion: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &FieldIDCheck{}
			report, err := New(Config{}, []Analyzer{EachFile(c)}).Run(tt.args.ctx, tt.args.view, tt.args.changeFiles)

			for key := range report {
				slices.SortStableFunc(report[key], func(a, b Diagnostic) int {
					if a.Span.Start.Line != b.Span.Start.Line {
						return a.Span.Start.Line - b.Span.Start.Line
					}

					return a.Span.Start.Col - b.Span.Start.Col
				})
			}

			got := make(map[uri.URI][]diagCmp, len(report))
			for key, ds := range report {
				got[key] = cmpAll(ds)
			}

			tt.assertion(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
