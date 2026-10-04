package analyzers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/analyzertest"
	"github.com/karitham/thrift-ls/sema"
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

	report := analyzertest.Run(t, sema.EachFile(&FieldIDCheck{}), map[string]string{
		"user.thrift": file1,
	}, "user.thrift")

	want := map[uri.URI][]analyzertest.Diag{
		analyzertest.URI("user.thrift"): {
			// struct
			{
				StartLine: 1 + 1, StartCol: 2 + 1, EndLine: 1 + 1, EndCol: 3 + 1,
				Severity: sema.SeverityError,
				Code:     sema.CodeFieldIDConflict,
				Message:  "field id conflict",
			},
			{
				StartLine: 2 + 1, StartCol: 2 + 1, EndLine: 2 + 1, EndCol: 3 + 1,
				Severity: sema.SeverityError,
				Code:     sema.CodeFieldIDConflict,
				Message:  "field id conflict",
			},
			{
				StartLine: 3 + 1, StartCol: 2 + 1, EndLine: 3 + 1, EndCol: 3 + 1,
				Severity: sema.SeverityError,
				Code:     sema.CodeFieldIDRange,
				Message:  "field id should be a positive integer in [1, 32767]",
			},
			{
				StartLine: 4 + 1, StartCol: 2 + 1, EndLine: 4 + 1, EndCol: 7 + 1,
				Severity: sema.SeverityError,
				Code:     sema.CodeFieldIDRange,
				Message:  "field id should be a positive integer in [1, 32767]",
			},

			// union
			{
				StartLine: 8 + 1, StartCol: 2 + 1, EndLine: 8 + 1, EndCol: 3 + 1,
				Severity: sema.SeverityError,
				Code:     sema.CodeFieldIDConflict,
				Message:  "field id conflict",
			},
			{
				StartLine: 9 + 1, StartCol: 2 + 1, EndLine: 9 + 1, EndCol: 3 + 1,
				Severity: sema.SeverityError,
				Code:     sema.CodeFieldIDConflict,
				Message:  "field id conflict",
			},
			{
				StartLine: 10 + 1, StartCol: 2 + 1, EndLine: 10 + 1, EndCol: 3 + 1,
				Severity: sema.SeverityError,
				Code:     sema.CodeFieldIDRange,
				Message:  "field id should be a positive integer in [1, 32767]",
			},
			{
				StartLine: 11 + 1, StartCol: 2 + 1, EndLine: 11 + 1, EndCol: 7 + 1,
				Severity: sema.SeverityError,
				Code:     sema.CodeFieldIDRange,
				Message:  "field id should be a positive integer in [1, 32767]",
			},

			// exception
			{
				StartLine: 15 + 1, StartCol: 2 + 1, EndLine: 15 + 1, EndCol: 3 + 1,
				Severity: sema.SeverityError,
				Code:     sema.CodeFieldIDConflict,
				Message:  "field id conflict",
			},
			{
				StartLine: 16 + 1, StartCol: 2 + 1, EndLine: 16 + 1, EndCol: 3 + 1,
				Severity: sema.SeverityError,
				Code:     sema.CodeFieldIDConflict,
				Message:  "field id conflict",
			},
			{
				StartLine: 17 + 1, StartCol: 2 + 1, EndLine: 17 + 1, EndCol: 3 + 1,
				Severity: sema.SeverityError,
				Code:     sema.CodeFieldIDRange,
				Message:  "field id should be a positive integer in [1, 32767]",
			},
			{
				StartLine: 18 + 1, StartCol: 2 + 1, EndLine: 18 + 1, EndCol: 7 + 1,
				Severity: sema.SeverityError,
				Code:     sema.CodeFieldIDRange,
				Message:  "field id should be a positive integer in [1, 32767]",
			},

			// function params
			{
				StartLine: 22 + 1, StartCol: 12 + 1, EndLine: 22 + 1, EndCol: 13 + 1,
				Severity: sema.SeverityError,
				Code:     sema.CodeFieldIDRange,
				Message:  "field id should be a positive integer in [1, 32767]",
			},
			{
				StartLine: 22 + 1, StartCol: 24 + 1, EndLine: 22 + 1, EndCol: 25 + 1,
				Severity: sema.SeverityError,
				Code:     sema.CodeFieldIDConflict,
				Message:  "field id conflict",
			},
			{
				StartLine: 22 + 1, StartCol: 39 + 1, EndLine: 22 + 1, EndCol: 40 + 1,
				Severity: sema.SeverityError,
				Code:     sema.CodeFieldIDConflict,
				Message:  "field id conflict",
			},
			{
				StartLine: 22 + 1, StartCol: 54 + 1, EndLine: 22 + 1, EndCol: 59 + 1,
				Severity: sema.SeverityError,
				Code:     sema.CodeFieldIDRange,
				Message:  "field id should be a positive integer in [1, 32767]",
			},

			// function throws
			{
				StartLine: 23 + 1, StartCol: 35 + 1, EndLine: 23 + 1, EndCol: 36 + 1,
				Severity: sema.SeverityError,
				Code:     sema.CodeFieldIDRange,
				Message:  "field id should be a positive integer in [1, 32767]",
			},
			{
				StartLine: 23 + 1, StartCol: 48 + 1, EndLine: 23 + 1, EndCol: 49 + 1,
				Severity: sema.SeverityError,
				Code:     sema.CodeFieldIDConflict,
				Message:  "field id conflict",
			},
			{
				StartLine: 23 + 1, StartCol: 62 + 1, EndLine: 23 + 1, EndCol: 63 + 1,
				Severity: sema.SeverityError,
				Code:     sema.CodeFieldIDConflict,
				Message:  "field id conflict",
			},
			{
				StartLine: 23 + 1, StartCol: 76 + 1, EndLine: 23 + 1, EndCol: 81 + 1,
				Severity: sema.SeverityError,
				Code:     sema.CodeFieldIDRange,
				Message:  "field id should be a positive integer in [1, 32767]",
			},
		},
	}

	got := make(map[uri.URI][]analyzertest.Diag, len(report))
	for key, ds := range report {
		got[key] = analyzertest.Simplify(ds)
	}

	assert.Equal(t, want, got)

	cleanReport := analyzertest.Run(t, sema.EachFile(&FieldIDCheck{}), map[string]string{
		"clean.thrift": "struct Clean {\n  1: required string name,\n  2: optional i32 id,\n}\n",
	}, "clean.thrift")
	assert.Empty(t, cleanReport[analyzertest.URI("clean.thrift")])
}

// Test_FieldIDCheck_OversizedID pins that field ids too large for int32
// under base-0 parsing (2147483648, 0xFFFFFFFF) are reported like any
// other out-of-range id instead of being dropped.
func Test_FieldIDCheck_OversizedID(t *testing.T) {
	content := `struct Big {
  2147483648: i32 a,
  0xFFFFFFFF: i32 b,
}
`

	report := analyzertest.Run(t, sema.EachFile(&FieldIDCheck{}), map[string]string{
		"user.thrift": content,
	}, "user.thrift")

	want := []analyzertest.Diag{
		{
			StartLine: 1 + 1, StartCol: 2 + 1, EndLine: 1 + 1, EndCol: 12 + 1,
			Severity: sema.SeverityError,
			Code:     sema.CodeFieldIDRange,
			Message:  "field id should be a positive integer in [1, 32767]",
		},
		{
			StartLine: 2 + 1, StartCol: 2 + 1, EndLine: 2 + 1, EndCol: 12 + 1,
			Severity: sema.SeverityError,
			Code:     sema.CodeFieldIDRange,
			Message:  "field id should be a positive integer in [1, 32767]",
		},
	}

	assert.Equal(t, want, analyzertest.Simplify(report[analyzertest.URI("user.thrift")]))
}

// Test_FieldIDCheck_DeterministicOrder pins emission order: field ids are
// grouped in a map whose iteration order varies per run, and a single run
// over four diagnostic groups can come out sorted by luck, hence ten runs.
func Test_FieldIDCheck_DeterministicOrder(t *testing.T) {
	content := `struct S {
  0: i32 a,
  32768: i32 b,
  1: i32 c,
  1: i32 d,
}
`

	want := []analyzertest.Diag{
		{
			StartLine: 1 + 1, StartCol: 2 + 1, EndLine: 1 + 1, EndCol: 3 + 1,
			Severity: sema.SeverityError,
			Code:     sema.CodeFieldIDRange,
			Message:  "field id should be a positive integer in [1, 32767]",
		},
		{
			StartLine: 2 + 1, StartCol: 2 + 1, EndLine: 2 + 1, EndCol: 7 + 1,
			Severity: sema.SeverityError,
			Code:     sema.CodeFieldIDRange,
			Message:  "field id should be a positive integer in [1, 32767]",
		},
		{
			StartLine: 3 + 1, StartCol: 2 + 1, EndLine: 3 + 1, EndCol: 3 + 1,
			Severity: sema.SeverityError,
			Code:     sema.CodeFieldIDConflict,
			Message:  "field id conflict",
		},
		{
			StartLine: 4 + 1, StartCol: 2 + 1, EndLine: 4 + 1, EndCol: 3 + 1,
			Severity: sema.SeverityError,
			Code:     sema.CodeFieldIDConflict,
			Message:  "field id conflict",
		},
	}

	for range 10 {
		report := analyzertest.Run(t, sema.EachFile(&FieldIDCheck{}), map[string]string{
			"user.thrift": content,
		}, "user.thrift")

		assert.Equal(t, want, analyzertest.Simplify(report[analyzertest.URI("user.thrift")]))
	}
}
