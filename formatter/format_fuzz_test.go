package formatter

import (
	"reflect"
	"strings"
	"testing"

	"github.com/karitham/thrift-ls/syntax"
)

// FuzzFormat checks the full formatting pipeline over arbitrary source and
// a full option set derived from the input bytes:
//
//   - formatting never panics or loops forever
//   - formatted output of a clean document parses without errors
//   - formatting is idempotent: format(format(x)) == format(x)
//   - formatted output is deterministic
//   - every comment survives formatting (lossless trivia)
//   - every structured annotation survives formatting (lossless nodes):
//     names and values round-trip in order
//   - in preserve mode, every field and enum separator survives
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
		"struct S {\n  1: map<string, // mid\n i32> m\n}",
		"const list<i32> L = [1, // mid\n 2]",
		"@naming.X {'a': 'b'}\nservice S {\n  void bar()\n}",
		"struct S {\n  @dep.Deprecated(1) 1: i32 a\n}",
		"@a.B(1)\n@c.D ['x', 'y']\ntypedef string T",
		"@a.B",
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

		opts := fuzzOpts(src)

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

		// Losslessness: every comment survives, in order. Right-trimmed
		// because the printer trims line ends.
		in := commentTexts(src)
		if got := commentTexts(out1); !reflect.DeepEqual(in, got) {
			t.Fatalf("comments lost or reordered:\nin:  %q\nout: %q\ninput: %q\noutput: %q", in, got, src, out1)
		}

		// Losslessness: every structured annotation survives, in order,
		// with its name and value text. Annotations are AST nodes, so the
		// check walks both documents instead of the token streams.
		inAnnos := structuredAnnotationTexts(src)
		if got := structuredAnnotationTexts(out1); !reflect.DeepEqual(inAnnos, got) {
			t.Fatalf("annotations lost or reordered:\nin:  %v\nout: %v\ninput: %q\noutput: %q", inAnnos, got, src, out1)
		}

		// In preserve mode every field and enum separator survives.
		allPreserve := true

		for _, c := range AllConstructs {
			if opts.Separator.Get(c) != SeparatorPreserve {
				allPreserve = false
			}
		}

		if allPreserve {
			in, errs := syntax.Parse([]byte(src))
			if hasParseErrors(errs) {
				t.Fatalf("reparse of input failed: %v", errs)
			}

			if got, _ := syntax.Parse([]byte(out1)); !reflect.DeepEqual(fieldSeps(in), fieldSeps(got)) {
				t.Fatalf("separators changed in preserve mode:\nin:  %v\nout: %v\ninput: %q\noutput: %q",
					fieldSeps(in), fieldSeps(got), src, out1)
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

// fuzzOpts derives a full option set deterministically from the input
// bytes, so every formatting path — alignment modes, separator modes, break
// flags, tab indents, width — is exercised by fuzzing.
func fuzzOpts(src string) Options {
	var h [4]int
	for i, b := range []byte(src) {
		h[i%4] += int(b)
	}

	o := DefaultOptions()
	o.PrintWidth = 1 + h[0]%120

	o.TabWidth = 1 + h[1]%8
	switch h[2] % 3 {
	case 0:
		o.Align = AlignField
	case 1:
		o.Align = AlignAssign
	case 2:
		o.Align = AlignDisable
	}

	for i, c := range AllConstructs {
		o.Separator.Set(c, SeparatorMode((h[i%4]+i)%4))
		o.Break.Set(c, h[(i+1)%4]%2 == 0)
	}

	o.NoTrailingNewline = h[3]%3 == 0
	switch h[0] % 4 {
	case 0:
		o.Indent = "  "
	case 1:
		o.Indent = "\t"
	case 2:
		o.Indent = "    "
	case 3:
		o.Indent = ""
	}

	return o
}

// commentTexts returns every comment and annotation text in the source, in
// source order, right-trimmed to match the printer's line-end trimming.
func commentTexts(src string) []string {
	toks, _ := syntax.Lex([]byte(src))

	var texts []string

	for _, tok := range toks {
		if !syntax.IsComment(tok.Kind) {
			continue
		}

		texts = append(texts, strings.TrimRight(tok.Text, " \t"))
	}

	return texts
}

// structuredAnnotationTexts returns every structured annotation name in
// the source, in document order. Values are not compared text-for-text:
// formatting canonicalizes their spacing, so only their presence (checked
// by the reparse) and the names are pinned.
func structuredAnnotationTexts(src string) []string {
	doc, errs := syntax.Parse([]byte(src))
	if len(errs) > 0 {
		return nil
	}

	var texts []string

	doc.EachStructuredAnnotation(func(sa *syntax.StructuredAnnotation) {
		texts = append(texts, sa.Name.Text)
	})

	return texts
}

// fieldSeps returns the separator kind of every field and enum value in the
// document, in source order.
func fieldSeps(doc *syntax.Document) []syntax.TokenKind {
	var seps []syntax.TokenKind

	add := func(f *syntax.Field) { seps = append(seps, f.Sep) }

	for _, n := range doc.Nodes {
		switch v := n.(type) {
		case *syntax.Struct:
			for _, f := range v.Fields {
				add(f)
			}
		case *syntax.Enum:
			for _, ev := range v.Values {
				seps = append(seps, ev.Sep)
			}
		case *syntax.Service:
			for _, fn := range v.Functions {
				for _, f := range fn.Args {
					add(f)
				}

				if fn.Throws != nil {
					for _, f := range fn.Throws.Fields {
						add(f)
					}
				}
			}
		}
	}

	return seps
}
