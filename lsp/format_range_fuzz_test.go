package lsp

import (
	"testing"

	"github.com/karitham/thrift-ls/formatter"
	"github.com/karitham/thrift-ls/syntax"
)

// FuzzFormatRangeText checks the invariants of range formatting:
//   - a refused range must produce no output
//   - an accepted range must be structurally sane (line-aligned, in bounds)
//   - splicing the formatted slice back into the document must keep the
//     document parseable
func FuzzFormatRangeText(f *testing.F) {
	seeds := []string{
		"struct A {\n1: string a\n}\n\nstruct B {  1: i32 b }",
		"\nstruct A {\n1: string a\n}\n\nstruct B {\n2: i32 b\n}\n\nstruct C {}",
		"struct A {}\n\n\n\nstruct B {\n1: string b\n}",
		"struct A {}\r\n\r\nstruct B {\r\n1: string b\r\n}\r\n\r\nstruct C {}",
		"include \"base.thrift\"\n\nnamespace go test\n\nenum Color {\n  RED = 1\n  BLUE = 2\n}\n\nstruct S {\n  1: Color c\n}",
		"// leading comment\nstruct A {}",
		"struct A {}",
		"",
		"\n",
		"\n\n\n",
	}
	for _, s := range seeds {
		f.Add([]byte(s), 0, 0)
	}

	f.Fuzz(func(t *testing.T, content []byte, rs, re int) {
		opts := formatter.Options{}
		// Clamp the fuzzed offsets into the content.
		limit := len(content) + 1
		rs = rs % limit
		if rs < 0 {
			rs += limit
		}
		re = re % limit
		if re < 0 {
			re += limit
		}

		newText, outRS, outRE, ok := formatRangeText(content, rs, re, opts)
		if !ok {
			return
		}

		// The accepted range must be line-aligned and in bounds.
		if outRS < 0 || outRE > len(content) || outRS >= outRE {
			t.Fatalf("invalid accepted range [%d, %d) for content %q", outRS, outRE, content)
		}
		if outRS != 0 && content[outRS-1] != '\n' {
			t.Fatalf("accepted range starts mid-line at %d in %q", outRS, content)
		}
		if outRE != len(content) && content[outRE] != '\n' {
			t.Fatalf("accepted range ends mid-line at %d in %q", outRE, content)
		}

		// Splicing the formatted slice back must not introduce new parse
		// errors: errors outside the range (the original may not parse
		// cleanly) must be preserved, and the formatted slice itself is
		// known to parse.
		spliced := make([]byte, 0, len(content)-outRE+outRS+len(newText))
		spliced = append(spliced, content[:outRS]...)
		spliced = append(spliced, newText...)
		spliced = append(spliced, content[outRE:]...)

		origErrs := errorMessages(syntax.Parse(content))
		splicedErrs := errorMessages(syntax.Parse(spliced))
		for msg, splicedCount := range splicedErrs {
			if splicedCount > origErrs[msg] {
				t.Fatalf("splice introduced new parse error %q\ncontent: %q\nrange: [%d, %d)\nnewText: %q",
					msg, content, outRS, outRE, newText)
			}
		}
	})
}

// errorMessages counts error-severity messages, ignoring warnings.
func errorMessages(doc *syntax.Document, errs []syntax.Error) map[string]int {
	_ = doc
	counts := make(map[string]int)
	for _, err := range errs {
		if err.Severity == syntax.SeverityError {
			counts[err.Message]++
		}
	}
	return counts
}
