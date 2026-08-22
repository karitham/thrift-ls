package formatter

import (
	"strings"
	"testing"

	"github.com/karitham/thrift-ls/options"
)

// sweepSeparatorSpace asserts the invariant behind the "space before
// separator" bug report: a trailing field/enum separator must hug its
// preceding token. A space between content and an emitted ; or , is only
// legal inside string literals and comments, so the check strips those and
// fails on anything left over.
//
// The sweep covers the full option matrix: every construct's separator
// mode (comma/semicolon/none/preserve) crossed with align modes, forced
// breaks, print widths, and two format passes (some artifacts only appear
// when formatting already-formatted output).

type sepCase struct {
	name string
	src  string
}

func sepSweepSources() []sepCase {
	return []sepCase{
		{"struct basic", `struct S {
  1: required i32 id;
  2: optional string name,
  3: map<string, i32> m = {"a": 1};
  4: bool flag
}`}, {"struct ragged widths", `struct R {
  1: i32 a,
  2: map<string, list<i64>> longer_field_name,
  3: i8 x = 5;
  4: set<string> s
}`},
		{"struct no separators at all", `struct N {
  1: i32 a
  2: i32 b
  3: i32 c
}`},
		{"struct trailing last", `struct T {
  1: i32 a;
  2: i32 b;
}`},
		{"comments around separators", `struct C {
  1: i32 id /* c */ ;
  // own line
  2: string name; // after sep
  3: i64 n
  ;
}`},
		{"annotations then separator", `struct AN {
  1: i32 id (js.type = "i32");
  2: string s (opt = "x")
}`},
		{"values with punctuation", `struct V {
  1: map<string, list<i32>> m = {"k": [1, 2]};
  2: i64 big = SOME_CONST;
}`},
		{"single line struct", `struct Tiny { 1: i32 id; 2: string name }`},
		{"union and exception", `union U {
  1: string a;
  2: i32 b
}

exception E {
  1: string msg;
}`},
		{"enum mixed separators", `enum M {
  A = 1;
  B = 2,
  C
}`},
		{"function args and throws", `service S {
  R get(1: i32 id; 2: string name) throws (1: E err);
  void put(1: R r)
}`},
		{"nested containers", `struct D {
  1: map<string, map<i32, list<set<double>>>> deep;
}`},
		{"pre-spaced separators struct", `struct P {
  1: i32 id ;
  2: string name   ,
  3: bool flag ;
  4: i64 n  ;
}`},
		{"pre-spaced separators enum", `enum PE {
  A = 1 ;
  B = 2  ;
  C
}`},
		{"pre-spaced separators service", `service PS {
  R get(1: i32 id ; 2: string name ) throws (1: E err ; 2: i32 code );
  void put(1: R r )
}`},
		{"pre-spaced collections", `struct PC {
  1: list<i32> l = [1 , 2 , 3];
  2: map<string, i32> m = {"a" : 1 , "b": 2 };
}`},
		{"enum long names mixed valued", `enum RelicKind {
  STAR_COMPASS,
  BLAZE_REAP = 2,
  UNHEARD_BELL,
  GAVEL_OF_THE_ABYSS = 4,
  CROSSED_STILLS
}`},
	}
}

// stripLiteralsAndComments replaces string literal and comment contents
// with placeholders so the separator scan sees only structural text while
// token adjacency (and therefore bogus "space before ;" seams) survives.
func stripLiteralsAndComments(s string) string {
	var b strings.Builder

	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '"':
			b.WriteString(`"S"`)

			j := i + 1
			for j < len(s) && s[j] != '"' {
				if s[j] == '\\' {
					j++
				}

				j++
			}

			i = j + 1
		case strings.HasPrefix(s[i:], "//"):
			b.WriteString("#C")

			for j := i; j < len(s); j++ {
				if s[j] == '\n' {
					break
				}

				i = j
			}

			i++
		case strings.HasPrefix(s[i:], "/*"):
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				b.WriteString("#C")
				i = len(s)

				break
			}

			b.WriteString("#C#C")
			i += end + 4
		default:
			b.WriteByte(c)
			i++
		}
	}

	return b.String()
}

// assertNoGapBeforeSep fails when a non-string/comment region contains
// whitespace immediately before an emitted , or ;.
func assertNoGapBeforeSep(t *testing.T, out string, label string) {
	t.Helper()

	stripped := stripLiteralsAndComments(out)

	lines := strings.Split(stripped, "\n")
	for li, line := range lines {
		idx := 0
		for idx < len(line) {
			p := strings.IndexAny(line[idx:], ",;")
			if p < 0 {
				break
			}

			at := idx + p

			if at > 0 {
				prev := line[at-1]
				if prev == ' ' || prev == '\t' {
					t.Errorf("%s: whitespace before %q at line %d:\n%s", label, rune(line[at]), li+1, out)

					return
				}
			}

			idx = at + 1
		}
	}
}

func TestNoSpaceBeforeSeparator(t *testing.T) {
	modes := []struct {
		name string
		mode SeparatorMode
	}{
		{"comma", SeparatorComma},
		{"semicolon", SeparatorSemicolon},
		{"none", SeparatorNone},
		{"preserve", SeparatorPreserve},
	}

	aligns := []struct {
		name  string
		align AlignMode
	}{
		{"field", AlignField},
		{"assign", AlignAssign},
		{"disable", AlignDisable},
	}

	widths := []int{20, 40, 80}

	for _, src := range sepSweepSources() {
		for _, m := range modes {
			for _, a := range aligns {
				for _, w := range widths {
					o := DefaultOptions()
					o.PrintWidth = w
					o.Separator.Set(options.ConstructStruct, m.mode)
					o.Separator.Set(options.ConstructEnum, m.mode)
					o.Separator.Set(options.ConstructArguments, m.mode)
					o.Separator.Set(options.ConstructThrows, m.mode)
					o.Align = a.align

					label := src.name + "/" + m.name + "/" + a.name

					got, err := Format(parseDoc(t, src.src), o)
					if err != nil {
						t.Fatalf("%s: Format: %v", label, err)
					}

					assertNoGapBeforeSep(t, got, label+" pass1")

					again, err := Format(parseDoc(t, got), o)
					if err != nil {
						t.Fatalf("%s pass2: parse: %v\noutput was:\n%s", label, err, got)
					}

					assertNoGapBeforeSep(t, again, label+" pass2")
				}
			}
		}
	}
}
