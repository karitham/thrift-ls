package syntax

import (
	"reflect"
	"testing"
	"unicode/utf8"
)

// FuzzLex checks lexer invariants that hold for every input:
//
//   - lexing always terminates with an EOF token (no infinite loops)
//   - lexing never panics, even on truncated, binary, or invalid UTF-8 input
//   - lexing is deterministic
//   - token and trivia texts are exact slices of the source
//   - reported line/col positions match the source bytes
//   - trailing trivia always starts on its token's line
func FuzzLex(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(""),
		[]byte("  \n\t\r\n"),
		[]byte("struct S {\n  1: i32 id\n}"),
		[]byte("service Foo extends Bar {\n  oneway void ping() throws (E e)\n}"),
		[]byte("const string s = \"héllo\"\nconst double d = -1.5e-3"),
		[]byte("// c\n# d\n/* e */\n/** doc */\n/**/"),
		[]byte("\"unterminated"),
		[]byte("\"trailing backslash\\"),
		[]byte("1. + - @ . : ; , < > [ ] { } ( ) = & *"),
		[]byte("\r\n\r\n"),
		[]byte("0x1F -0x10 .5 1e10 1. 0x e10"),
		[]byte("include \"foo.thrift\"\ncpp_include \"bar.h\""),
		[]byte("enum E { A = 1, B, C = 0x10 }"),
		[]byte("typedef map<string, list<i32>> M"),
		[]byte(`"a\qb"`),
		[]byte("/* unterminated"),
		[]byte("foo.bar baz1 _x"),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, src []byte) {
		toks, errs := Lex(src)
		srcStr := string(src)

		if len(toks) == 0 || toks[len(toks)-1].Kind != TokenEOF {
			t.Fatalf("lexing did not terminate with an EOF token: %d tokens", len(toks))
		}
		// Every real token consumes at least one byte; only EOF is empty.
		if len(toks) > len(src)+1 {
			t.Fatalf("more tokens than input bytes: %d tokens for %d bytes", len(toks), len(src))
		}

		// Determinism.
		toks2, errs2 := Lex(src)
		if !reflect.DeepEqual(toks, toks2) || !reflect.DeepEqual(errs, errs2) {
			t.Fatalf("lexing is not deterministic")
		}

		// Errors are in bounds and positioned correctly.
		for _, err := range errs {
			if err.Offset < 0 || err.Offset > len(src) {
				t.Fatalf("error offset %d out of range", err.Offset)
			}

			checkPos(t, srcStr, err.Offset, err.Line, err.Col, "error")
		}

		// Token and trivia invariants.
		prevEnd := 0

		for i, tok := range toks {
			if tok.Kind == TokenInvalid {
				t.Fatalf("token %d has invalid kind", i)
			}

			if tok.Offset < 0 || tok.Offset+len(tok.Text) > len(src) {
				t.Fatalf("token %d (%s) spans outside the source", i, tok.Kind)
			}

			if got := src[tok.Offset : tok.Offset+len(tok.Text)]; string(got) != tok.Text {
				t.Fatalf("token %d text %q does not match source %q", i, tok.Text, got)
			}

			checkPos(t, srcStr, tok.Offset, tok.Line, tok.Col, "token")

			if tok.BlankLinesBefore < 0 {
				t.Fatalf("token %d has negative BlankLinesBefore", i)
			}

			// Leading trivia lies between the previous token and this one.
			for _, tr := range tok.Leading {
				if tr.Offset < prevEnd || tr.Offset+len(tr.Text) > tok.Offset || tr.Offset+len(tr.Text) > len(src) {
					t.Fatalf("leading trivia %q of token %d outside the gap [%d, %d)", tr.Text, i, prevEnd, tok.Offset)
				}

				checkPos(t, srcStr, tr.Offset, tr.Line, tr.Col, "leading trivia")
			}

			for _, tr := range tok.Trailing {
				if tr.Offset < tok.Offset || tr.Offset+len(tr.Text) > len(src) {
					t.Fatalf("trailing trivia %q of token %d outside the source", tr.Text, i)
				}

				checkPos(t, srcStr, tr.Offset, tr.Line, tr.Col, "trailing trivia")

				if tr.Line != tok.Line {
					t.Fatalf("trailing trivia %q of token %d starts on line %d, token is on line %d",
						tr.Text, i, tr.Line, tok.Line)
				}
			}

			if tok.Kind != TokenEOF {
				prevEnd = tok.Offset + len(tok.Text)
				for _, tr := range tok.Trailing {
					prevEnd += len(tr.Text)
				}
			}
		}
	})
}

// checkPos verifies that the lexer's line/col bookkeeping matches a naive
// scan of the source: \n (and lone \r, but not \r\n) start a new line, and
// columns count runes.
func checkPos(t *testing.T, src string, offset, line, col int, what string) {
	t.Helper()

	gotLine, gotCol := lineColAt(src, offset)
	if gotLine != line || gotCol != col {
		t.Fatalf("%s at offset %d: reported %d:%d, want %d:%d", what, offset, line, col, gotLine, gotCol)
	}
}

func lineColAt(src string, offset int) (line, col int) {
	line, col = 1, 1

	for i := 0; i < offset; {
		switch src[i] {
		case '\n':
			line++
			col = 1
			i++
		case '\r':
			if i+1 < offset && src[i+1] == '\n' {
				i++ // \r\n: the \n resets the line

				continue
			}

			line++
			col = 1
			i++
		default:
			_, size := utf8.DecodeRuneInString(src[i:offset])
			col++
			i += size
		}
	}

	return line, col
}
