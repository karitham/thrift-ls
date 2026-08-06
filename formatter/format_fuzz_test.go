package formatter

import (
	"testing"

	"github.com/karitham/thrift-ls/syntax"
)

// FuzzFormat checks the full formatting pipeline over arbitrary source:
//
//   - formatting never panics or loops forever
//   - formatted output of a clean document parses without errors
//   - formatting is idempotent: format(format(x)) == format(x)
//   - formatted output is deterministic
func FuzzFormat(f *testing.F) {
	for _, seed := range []string{
		"",
		"struct S {\n  1: i32 a\n}",
		"service S {\n  i32 f(1: i64 a, 2: string b) throws (E e)\n}",
		"const map<string, list<i32>> m = {\"a\": [1, 2]}",
		"enum E {\n  A = 1,\n  B\n} (tag = \"x\")",
		"// c\nstruct S {\n  1: i32 a // eol\n}",
		"typedef i32 T (bare, a = \"1\"; b = '2')",
		"include \"a.thrift\"\nnamespace go x",
		"struct S {",
		"service S {\n  void f(1: i32 a // c\n  )\n}",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, src string) {
		doc, errs := syntax.Parse([]byte(src))
		for _, err := range errs {
			if err.Severity == syntax.SeverityError {
				return // only format clean documents
			}
		}

		opts := testOpts(1 + len(src)%100)
		out1, err := Format(doc, opts)
		if err != nil {
			t.Fatalf("Format: %v", err)
		}

		// The output must parse cleanly.
		_, errs2 := syntax.Parse([]byte(out1))
		for _, err := range errs2 {
			if err.Severity == syntax.SeverityError {
				t.Fatalf("formatted output does not parse: %v\ninput: %q\noutput: %q", errs2, src, out1)
			}
		}

		// Idempotency and determinism.
		out2, err := Format(doc, opts)
		if err != nil {
			t.Fatalf("Format (second): %v", err)
		}
		if out1 != out2 {
			t.Fatalf("Format is not deterministic:\nfirst: %q\nsecond: %q", out1, out2)
		}
		doc3, errs3 := syntax.Parse([]byte(out1))
		for _, err := range errs3 {
			if err.Severity == syntax.SeverityError {
				t.Fatalf("re-parse failed: %v", errs3)
			}
		}
		out3, err := Format(doc3, opts)
		if err != nil {
			t.Fatalf("Format (third): %v", err)
		}
		if out1 != out3 {
			t.Fatalf("not idempotent:\nfirst: %q\nsecond: %q", out1, out3)
		}
	})
}
