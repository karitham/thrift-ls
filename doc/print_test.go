package doc

import (
	"testing"
)

// T builds a Text from a string.
func T(s string) Doc { return Text(s) }

// testOpts are the defaults used by most cases.
func testOpts(width int) Options {
	return Options{PrintWidth: width, Indent: "  ", TabWidth: 2, NewLine: "\n"}
}

func printT(t *testing.T, d Doc, o Options) string {
	t.Helper()

	got, err := Print(d, o)
	if err != nil {
		t.Fatalf("Print: %v", err)
	}

	return got
}

func TestPrintText(t *testing.T) {
	tests := []struct {
		name string
		doc  func() Doc
		want string
	}{
		{"empty", func() Doc { return Concat{} }, ""},
		{"text", func() Doc { return T("hello world") }, "hello world"},
		{"concat", func() Doc { return Concat{T("a"), T("b"), T("c")} }, "abc"},
		{"join", func() Doc { return Join(T(","), []Doc{T("a"), T("b"), T("c")}) }, "a,b,c"},
		{"join empty", func() Doc { return Join(T(","), nil) }, ""},
		{"group fits", func() Doc { return Group(Concat{T("hello"), T(" world")}) }, "hello world"},
		{"empty group", func() Doc { return Group(Concat{}) }, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := printT(t, tt.doc(), testOpts(80)); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrintLineBreaking(t *testing.T) {
	tests := []struct {
		name  string
		doc   func() Doc
		width int
		want  string
	}{
		{
			name:  "line stays flat when it fits",
			doc:   func() Doc { return Group(Concat{T("a"), Line, T("b")}) },
			width: 10,
			want:  "a b",
		},
		{
			name:  "line breaks when it does not fit",
			doc:   func() Doc { return Group(Concat{T("a"), Line, T("b")}) },
			width: 2,
			want:  "a\nb",
		},
		{
			name:  "line breaks exactly at boundary",
			doc:   func() Doc { return Group(Concat{T("ab"), Line, T("c")}) },
			width: 3,
			want:  "ab\nc", // "ab c" is 4 columns and does not fit
		},
		{
			name:  "softline is empty when flat",
			doc:   func() Doc { return Group(Concat{T("a"), SoftLine, T("b")}) },
			width: 80,
			want:  "ab",
		},
		{
			name:  "softline breaks with the group",
			doc:   func() Doc { return Group(Concat{T("a"), SoftLine, T("b")}) },
			width: 1,
			want:  "a\nb",
		},
		{
			name:  "canonical indent pattern",
			doc:   func() Doc { return Group(Concat{T("a"), Indent(Concat{Line, T("b")})}) },
			width: 10,
			want:  "a b",
		},
		{
			name:  "canonical indent pattern breaks",
			doc:   func() Doc { return Group(Concat{T("a"), Indent(Concat{Line, T("b")})}) },
			width: 2,
			want:  "a\n  b",
		},
		{
			name: "inner group stays flat inside broken outer",
			doc: func() Doc {
				return Group(Concat{
					T("a"),
					Group(Concat{T("b"), SoftLine, T("c")}),
					Line,
					T("d"),
				})
			},
			width: 4,
			want:  "abc\nd", // the inner group fits in the remaining width
		},
		{
			name: "inner group breaks when it does not fit",
			doc: func() Doc {
				return Group(Concat{
					T("aaaa"),
					Group(Concat{T("bbbb"), SoftLine, T("cc")}),
					Line,
					T("d"),
				})
			},
			width: 8,
			want:  "aaaabbbb\ncc\nd", // only the inner group's own line breaks
		},
		{
			name: "inner group breaks when remaining width is too small",
			doc: func() Doc {
				return Group(Concat{
					T("a"),
					Group(Concat{T("b"), SoftLine, T("c")}),
					Line,
					T("d"),
				})
			},
			width: 2,
			want:  "ab\nc\nd",
		},
		{
			name:  "hardline always breaks",
			doc:   func() Doc { return Group(Concat{T("a"), HardLineNoBreak, T("b")}) },
			width: 80,
			want:  "a\nb",
		},
		{
			name:  "hardline breaks enclosing group",
			doc:   func() Doc { return Group(Concat{T("a"), HardLine, T("b"), SoftLine, T("c")}) },
			width: 80,
			want:  "a\nb\nc",
		},
		{
			name: "break propagates through nested groups",
			doc: func() Doc {
				return Group(Concat{
					Group(Concat{T("x"), HardLine, T("y"), SoftLine, T("z")}),
					SoftLine,
					T("w"),
				})
			},
			width: 80,
			want:  "x\ny\nz\nw",
		},
		{
			name:  "forced break group",
			doc:   func() Doc { return GroupBreak(Concat{T("a"), SoftLine, T("b")}) },
			width: 80,
			want:  "a\nb",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := printT(t, tt.doc(), testOpts(tt.width)); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrintIfBreak(t *testing.T) {
	tests := []struct {
		name  string
		doc   func() Doc
		width int
		want  string
	}{
		{
			name:  "flat takes flat contents",
			doc:   func() Doc { return Group(Concat{T("a"), IfBreak(T(","), T("")), T("b")}) },
			width: 80,
			want:  "ab",
		},
		{
			name:  "broken takes break contents",
			doc:   func() Doc { return GroupBreak(Concat{T("a"), IfBreak(T(","), T("")), T("b")}) },
			width: 80,
			want:  "a,b",
		},
		{
			name: "ifBreak follows referenced group",
			doc: func() Doc {
				return Concat{
					GroupID(1, Concat{T("aaaa"), SoftLine, T("bbbb")}),
					IfBreakFor(T(","), T(""), 1),
				}
			},
			width: 80,
			want:  "aaaabbbb",
		},
		{
			name: "ifBreak follows broken referenced group",
			doc: func() Doc {
				return Concat{
					GroupID(1, Concat{T("aaaa"), SoftLine, T("bbbb")}),
					IfBreakFor(T(","), T(""), 1),
				}
			},
			width: 4,
			want:  "aaaa\nbbbb,",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := printT(t, tt.doc(), testOpts(tt.width)); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrintConditionalGroup(t *testing.T) {
	tests := []struct {
		name  string
		width int
		want  string
	}{
		{
			name:  "least expanded state fits",
			width: 5,
			want:  "a b c",
		},
		{
			name:  "middle state fits",
			width: 3,
			want:  "a b\nc",
		},
		{
			name:  "last state breaks",
			width: 2,
			want:  "a\nb\nc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := ConditionalGroup(0,
				Concat{T("a"), Line, T("b"), Line, T("c")},
				Concat{T("a"), Line, T("b"), HardLineNoBreak, T("c")},
				Concat{T("a"), HardLineNoBreak, T("b"), HardLineNoBreak, T("c")},
			)
			if got := printT(t, doc, testOpts(tt.width)); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrintIndentAndAlign(t *testing.T) {
	tests := []struct {
		name string
		doc  func() Doc
		want string
	}{
		{
			name: "indent applies at line breaks",
			doc: func() Doc {
				return GroupBreak(Concat{T("a"), Indent(Concat{Line, T("b"), Line, T("c")})})
			},
			want: "a\n  b\n  c",
		},
		{
			name: "align by columns",
			doc: func() Doc {
				return GroupBreak(Concat{T("a"), Align(4, Concat{Line, T("b")})})
			},
			want: "a\n    b",
		},
		{
			name: "align nests inside indent",
			doc: func() Doc {
				return GroupBreak(Concat{T("a"), Indent(Concat{Line, Align(2, Concat{T("b"), Line, T("c")})})})
			},
			want: "a\n  b\n    c",
		},
		{
			name: "nested indent accumulates",
			doc: func() Doc {
				return GroupBreak(Concat{T("a"), Indent(Indent(Concat{Line, T("b")}))})
			},
			want: "a\n    b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := printT(t, tt.doc(), testOpts(80)); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrintTabIndent(t *testing.T) {
	tests := []struct {
		name string
		doc  func() Doc
		want string
	}{
		{
			name: "tab indentation and measurement",
			doc: func() Doc {
				return GroupBreak(Concat{T("a"), Indent(Concat{Line, T("b")})})
			},
			want: "a\n\tb",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := Options{PrintWidth: 80, Indent: "\t", TabWidth: 4, NewLine: "\n"}
			if got := printT(t, tt.doc(), o); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrintLineSuffix(t *testing.T) {
	tests := []struct {
		name string
		doc  func() Doc
		want string
	}{
		{
			name: "suffix prints before the line break",
			doc: func() Doc {
				return GroupBreak(Concat{T("a"), LineSuffix(T(" // c")), Line, T("b")})
			},
			want: "a // c\nb",
		},
		{
			name: "suffix flushes at document end without a break",
			doc: func() Doc {
				return Concat{T("a"), LineSuffix(T(" // c"))}
			},
			want: "a // c",
		},
		{
			name: "boundary flushes pending suffixes",
			doc: func() Doc {
				return GroupBreak(Concat{
					T("a"), LineSuffix(T(" // first")), LineSuffixBoundary,
				})
			},
			// The boundary schedules a hard line that flushes the suffix and
			// ends the line, matching Prettier's boundary semantics.
			want: "a // first\n",
		},
		{
			name: "suffix counts against width",
			doc: func() Doc {
				return Group(Concat{T("aaaa"), LineSuffix(T(" // c")), Line, T("b")})
			},
			want: "aaaa b // c", // suffix width is not measured, like Prettier
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := printT(t, tt.doc(), testOpts(80)); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrintTrim(t *testing.T) {
	tests := []struct {
		name string
		doc  func() Doc
		want string
	}{
		{"trim removes trailing spaces", func() Doc { return Concat{T("a  "), TrimDoc, T("b")} }, "ab"},
		{"trim removes trailing tabs", func() Doc { return Concat{T("a\t\t"), TrimDoc, T("b")} }, "ab"},
		{"trim without trailing whitespace", func() Doc { return Concat{T("a"), TrimDoc, T("b")} }, "ab"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := printT(t, tt.doc(), testOpts(80)); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrintRemeasure(t *testing.T) {
	// A hard line printed inside a flat-measured group invalidates the
	// measurement; the next group must remeasure instead of trusting the
	// flat shortcut.
	doc := Group(Concat{
		T("a"), HardLineNoBreak,
		Group(Concat{T("bbbb"), Line, T("cc")}),
		Line,
		T("dd"),
	})
	got := printT(t, doc, testOpts(8))

	want := "a\nbbbb\ncc dd"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPrintNewLineOption(t *testing.T) {
	o := Options{PrintWidth: 2, Indent: "  ", TabWidth: 2, NewLine: "\r\n"}

	doc := GroupBreak(Concat{T("a"), Line, T("b")})
	if got := printT(t, doc, o); got != "a\r\nb" {
		t.Errorf("got %q, want %q", got, "a\r\nb")
	}
}

func TestPrintValidation(t *testing.T) {
	tests := []struct {
		name string
		opts Options
	}{
		{"zero width", Options{PrintWidth: 0, TabWidth: 2}},
		{"negative width", Options{PrintWidth: -1, TabWidth: 2}},
		{"zero tab width", Options{PrintWidth: 80, TabWidth: 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Print(T("x"), tt.opts); err == nil {
				t.Error("expected validation error")
			}
		})
	}
}

func TestPrintUnicodeWidth(t *testing.T) {
	tests := []struct {
		name  string
		doc   func() Doc
		width int
		want  string
	}{
		{
			name:  "wide characters count as two columns",
			doc:   func() Doc { return Group(Concat{T("日本語"), Line, T("x")}) },
			width: 8,
			want:  "日本語 x",
		},
		{
			name:  "wide characters break the group",
			doc:   func() Doc { return Group(Concat{T("日本語"), Line, T("x")}) },
			width: 7,
			want:  "日本語\nx",
		},
		{
			name:  "combining marks are zero width",
			doc:   func() Doc { return Group(Concat{T("e\u0301"), Line, T("x")}) },
			width: 3,
			want:  "e\u0301 x",
		},
		{
			name:  "emoji count as two columns",
			doc:   func() Doc { return Group(Concat{T("😀"), Line, T("x")}) },
			width: 4,
			want:  "😀 x",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := printT(t, tt.doc(), testOpts(tt.width)); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStringWidth(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"日本語", 6},
		{"aé", 2},
		{"e\u0301", 1},
		{"😀", 2},
		{"a\tb", 2}, // tab is a control character
		{"\x00\x1f\x7f", 0},
	}
	for _, tt := range tests {
		if got := stringWidth(tt.in); got != tt.want {
			t.Errorf("stringWidth(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestPrintIdempotentDocs(t *testing.T) {
	// These docs must print identically at the same width twice in a row;
	// break propagation must not change the result on the second pass.
	docs := []Doc{
		Group(Concat{T("a"), Line, T("b")}),
		Group(Concat{T("a"), HardLine, T("b"), SoftLine, T("c")}),
		ConditionalGroup(0,
			Concat{T("a"), Line, T("b"), Line, T("c")},
			Concat{T("a"), Line, T("b"), HardLineNoBreak, T("c")},
		),
	}
	for _, d := range docs {
		first := printT(t, d, testOpts(2))

		second := printT(t, d, testOpts(2))
		if first != second {
			t.Errorf("not idempotent: %q vs %q", first, second)
		}
	}
}

func TestDocMutability(t *testing.T) {
	// Print mutates the doc (break propagation); printing a fresh doc each
	// time must yield stable output.
	build := func() Doc {
		return Group(Concat{T("a"), HardLine, T("b"), SoftLine, T("c")})
	}

	first := printT(t, build(), testOpts(80))
	for i := range 10 {
		if got := printT(t, build(), testOpts(80)); got != first {
			t.Fatalf("iteration %d: got %q, want %q", i, got, first)
		}
	}
}
