package lsp

import (
	"strings"
	"testing"

	"github.com/karitham/thrift-ls/formatter"
	"github.com/karitham/thrift-ls/syntax"
)

// FuzzFormatRangeText checks the invariants of block-diff range formatting:
//   - a document the formatter refuses yields no edits
//   - an unchanged document yields no edits
//   - the edits are ordered, non-overlapping, line-aligned, and in bounds
//   - applying every edit reproduces the whole-document formatting
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
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, content []byte) {
		opts := formatter.Options{}

		doc, errs := syntax.Parse(content)
		if len(errs) > 0 {
			// The formatter refuses documents with errors: no edits.
			if edits := blockDiff(content, content); len(edits) != 0 {
				t.Fatalf("unparseable document produced edits")
			}

			return
		}

		formatted, err := formatter.Format(doc, opts)
		if err != nil {
			return
		}

		if string(content) == formatted {
			if edits := blockDiff(content, []byte(formatted)); len(edits) != 0 {
				t.Fatalf("no-op format produced edits for %q", content)
			}

			return
		}

		edits := blockDiff(content, []byte(formatted))
		if len(edits) == 0 {
			t.Fatalf("changed format produced no edits for %q", content)
		}

		// Edits are ordered, non-overlapping, line-aligned, and in bounds.
		prev := 0

		for _, e := range edits {
			if e.start < prev || e.end > len(content) || e.start >= e.end {
				t.Fatalf("invalid edit [%d, %d) in %q", e.start, e.end, content)
			}

			if e.start != 0 && content[e.start-1] != '\n' {
				t.Fatalf("edit starts mid-line at %d in %q", e.start, content)
			}

			if e.end != len(content) && content[e.end] != '\n' {
				t.Fatalf("edit ends mid-line at %d in %q", e.end, content)
			}

			prev = e.end
		}

		// Applying every edit reproduces the whole-document formatting.
		var sb strings.Builder
		prev = 0

		for _, e := range edits {
			sb.Write(content[prev:e.start])
			sb.WriteString(e.text)
			prev = e.end
		}

		sb.Write(content[prev:])

		if sb.String() != formatted {
			t.Fatalf("applying all edits != whole-document format\ncontent: %q\nformatted: %q\nedits: %+v", content, formatted, edits)
		}
	})
}
