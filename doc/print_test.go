package doc

import (
	"testing"
)

// testOpts are the defaults used by most cases.
func testOpts(width int) Options {
	return Options{PrintWidth: width, Indent: "  ", TabWidth: 2, NewLine: "\n"}
}

// printT builds the doc on a fresh arena and prints it.
func printT(t *testing.T, build func(a *Arena) Doc, o Options) string {
	t.Helper()

	var a Arena

	got, err := Print(build(&a), o)
	if err != nil {
		t.Fatalf("Print: %v", err)
	}

	return got
}

func TestPrintText(t *testing.T) {
	tests := []struct {
		name string
		doc  func(a *Arena) Doc
		want string
	}{
		{"empty", func(a *Arena) Doc { return a.Concat() }, ""},
		{"text", func(a *Arena) Doc { return a.Text("hello world") }, "hello world"},
		{"concat", func(a *Arena) Doc { return a.Concat(a.Text("a"), a.Text("b"), a.Text("c")) }, "abc"},
		{"join", func(a *Arena) Doc { return a.Join(a.Text(","), []Doc{a.Text("a"), a.Text("b"), a.Text("c")}) }, "a,b,c"},
		{"join empty", func(a *Arena) Doc { return a.Join(a.Text(","), nil) }, ""},
		{"group fits", func(a *Arena) Doc { return a.Group(a.Concat(a.Text("hello"), a.Text(" world"))) }, "hello world"},
		{"empty group", func(a *Arena) Doc { return a.Group(a.Concat()) }, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := printT(t, tt.doc, testOpts(80)); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrintLineBreaking(t *testing.T) {
	tests := []struct {
		name  string
		doc   func(a *Arena) Doc
		width int
		want  string
	}{
		{
			name:  "line stays flat when it fits",
			doc:   func(a *Arena) Doc { return a.Group(a.Concat(a.Text("a"), Line, a.Text("b"))) },
			width: 10,
			want:  "a b",
		},
		{
			name:  "line breaks when it does not fit",
			doc:   func(a *Arena) Doc { return a.Group(a.Concat(a.Text("a"), Line, a.Text("b"))) },
			width: 2,
			want:  "a\nb",
		},
		{
			name:  "line breaks exactly at boundary",
			doc:   func(a *Arena) Doc { return a.Group(a.Concat(a.Text("ab"), Line, a.Text("c"))) },
			width: 3,
			want:  "ab\nc", // "ab c" is 4 columns and does not fit
		},
		{
			name:  "softline is empty when flat",
			doc:   func(a *Arena) Doc { return a.Group(a.Concat(a.Text("a"), SoftLine, a.Text("b"))) },
			width: 80,
			want:  "ab",
		},
		{
			name:  "softline breaks with the group",
			doc:   func(a *Arena) Doc { return a.Group(a.Concat(a.Text("a"), SoftLine, a.Text("b"))) },
			width: 1,
			want:  "a\nb",
		},
		{
			name:  "canonical indent pattern",
			doc:   func(a *Arena) Doc { return a.Group(a.Concat(a.Text("a"), a.Indent(a.Concat(Line, a.Text("b"))))) },
			width: 10,
			want:  "a b",
		},
		{
			name:  "canonical indent pattern breaks",
			doc:   func(a *Arena) Doc { return a.Group(a.Concat(a.Text("a"), a.Indent(a.Concat(Line, a.Text("b"))))) },
			width: 2,
			want:  "a\n  b",
		},
		{
			name: "inner group stays flat inside broken outer",
			doc: func(a *Arena) Doc {
				return a.Group(a.Concat(
					a.Text("a"),
					a.Group(a.Concat(a.Text("b"), SoftLine, a.Text("c"))),
					Line,
					a.Text("d"),
				))
			},
			width: 4,
			want:  "abc\nd", // the inner group fits in the remaining width
		},
		{
			name: "inner group breaks when it does not fit",
			doc: func(a *Arena) Doc {
				return a.Group(a.Concat(
					a.Text("aaaa"),
					a.Group(a.Concat(a.Text("bbbb"), SoftLine, a.Text("cc"))),
					Line,
					a.Text("d"),
				))
			},
			width: 8,
			want:  "aaaabbbb\ncc\nd", // only the inner group's own line breaks
		},
		{
			name: "inner group breaks when remaining width is too small",
			doc: func(a *Arena) Doc {
				return a.Group(a.Concat(
					a.Text("a"),
					a.Group(a.Concat(a.Text("b"), SoftLine, a.Text("c"))),
					Line,
					a.Text("d"),
				))
			},
			width: 2,
			want:  "ab\nc\nd",
		},
		{
			name:  "hardline always breaks",
			doc:   func(a *Arena) Doc { return a.Group(a.Concat(a.Text("a"), HardLineNoBreak, a.Text("b"))) },
			width: 80,
			want:  "a\nb",
		},
		{
			name: "hardline breaks enclosing group",
			doc: func(a *Arena) Doc {
				return a.Group(a.Concat(a.Text("a"), HardLine, a.Text("b"), SoftLine, a.Text("c")))
			},
			width: 80,
			want:  "a\nb\nc",
		},
		{
			name: "break propagates through nested groups",
			doc: func(a *Arena) Doc {
				return a.Group(a.Concat(
					a.Group(a.Concat(a.Text("x"), HardLine, a.Text("y"), SoftLine, a.Text("z"))),
					SoftLine,
					a.Text("w"),
				))
			},
			width: 80,
			want:  "x\ny\nz\nw",
		},
		{
			name:  "forced break group",
			doc:   func(a *Arena) Doc { return a.GroupBreak(a.Concat(a.Text("a"), SoftLine, a.Text("b"))) },
			width: 80,
			want:  "a\nb",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := printT(t, tt.doc, testOpts(tt.width)); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrintIfBreak(t *testing.T) {
	tests := []struct {
		name  string
		doc   func(a *Arena) Doc
		width int
		want  string
	}{
		{
			name: "flat takes flat contents",
			doc: func(a *Arena) Doc {
				return a.Group(a.Concat(a.Text("a"), a.IfBreak(a.Text(","), a.Text("")), a.Text("b")))
			},
			width: 80,
			want:  "ab",
		},
		{
			name: "broken takes break contents",
			doc: func(a *Arena) Doc {
				return a.GroupBreak(a.Concat(a.Text("a"), a.IfBreak(a.Text(","), a.Text("")), a.Text("b")))
			},
			width: 80,
			want:  "a,b",
		},
		{
			name: "ifBreak follows referenced group",
			doc: func(a *Arena) Doc {
				return a.Concat(
					a.GroupID(1, a.Concat(a.Text("aaaa"), SoftLine, a.Text("bbbb"))),
					a.IfBreakFor(a.Text(","), a.Text(""), 1),
				)
			},
			width: 80,
			want:  "aaaabbbb",
		},
		{
			name: "ifBreak follows broken referenced group",
			doc: func(a *Arena) Doc {
				return a.Concat(
					a.GroupID(1, a.Concat(a.Text("aaaa"), SoftLine, a.Text("bbbb"))),
					a.IfBreakFor(a.Text(","), a.Text(""), 1),
				)
			},
			width: 4,
			want:  "aaaa\nbbbb,",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := printT(t, tt.doc, testOpts(tt.width)); got != tt.want {
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
			got := printT(t, func(a *Arena) Doc {
				return a.ConditionalGroup(0,
					a.Concat(a.Text("a"), Line, a.Text("b"), Line, a.Text("c")),
					a.Concat(a.Text("a"), Line, a.Text("b"), HardLineNoBreak, a.Text("c")),
					a.Concat(a.Text("a"), HardLineNoBreak, a.Text("b"), HardLineNoBreak, a.Text("c")),
				)
			}, testOpts(tt.width))
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrintIndentAndAlign(t *testing.T) {
	tests := []struct {
		name string
		doc  func(a *Arena) Doc
		want string
	}{
		{
			name: "indent applies at line breaks",
			doc: func(a *Arena) Doc {
				return a.GroupBreak(a.Concat(a.Text("a"), a.Indent(a.Concat(Line, a.Text("b"), Line, a.Text("c")))))
			},
			want: "a\n  b\n  c",
		},
		{
			name: "align by columns",
			doc: func(a *Arena) Doc {
				return a.GroupBreak(a.Concat(a.Text("a"), a.Align(4, a.Concat(Line, a.Text("b")))))
			},
			want: "a\n    b",
		},
		{
			name: "align nests inside indent",
			doc: func(a *Arena) Doc {
				return a.GroupBreak(a.Concat(a.Text("a"), a.Indent(a.Concat(Line, a.Align(2, a.Concat(a.Text("b"), Line, a.Text("c")))))))
			},
			want: "a\n  b\n    c",
		},
		{
			name: "nested indent accumulates",
			doc: func(a *Arena) Doc {
				return a.GroupBreak(a.Concat(a.Text("a"), a.Indent(a.Indent(a.Concat(Line, a.Text("b"))))))
			},
			want: "a\n    b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := printT(t, tt.doc, testOpts(80)); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrintTabIndent(t *testing.T) {
	tests := []struct {
		name string
		doc  func(a *Arena) Doc
		want string
	}{
		{
			name: "tab indentation and measurement",
			doc: func(a *Arena) Doc {
				return a.GroupBreak(a.Concat(a.Text("a"), a.Indent(a.Concat(Line, a.Text("b")))))
			},
			want: "a\n\tb",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := Options{PrintWidth: 80, Indent: "\t", TabWidth: 4, NewLine: "\n"}
			if got := printT(t, tt.doc, o); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrintLineSuffix(t *testing.T) {
	tests := []struct {
		name string
		doc  func(a *Arena) Doc
		want string
	}{
		{
			name: "suffix prints before the line break",
			doc: func(a *Arena) Doc {
				return a.GroupBreak(a.Concat(a.Text("a"), a.LineSuffix(a.Text(" // c")), Line, a.Text("b")))
			},
			want: "a // c\nb",
		},
		{
			name: "suffix flushes at document end without a break",
			doc: func(a *Arena) Doc {
				return a.Concat(a.Text("a"), a.LineSuffix(a.Text(" // c")))
			},
			want: "a // c",
		},
		{
			name: "boundary flushes pending suffixes",
			doc: func(a *Arena) Doc {
				return a.GroupBreak(a.Concat(
					a.Text("a"), a.LineSuffix(a.Text(" // first")), LineSuffixBoundary,
				))
			},
			// The boundary schedules a hard line that flushes the suffix and
			// ends the line, matching Prettier's boundary semantics.
			want: "a // first\n",
		},
		{
			name: "suffix counts against width",
			doc: func(a *Arena) Doc {
				return a.Group(a.Concat(a.Text("aaaa"), a.LineSuffix(a.Text(" // c")), Line, a.Text("b")))
			},
			want: "aaaa b // c", // suffix width is not measured, like Prettier
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := printT(t, tt.doc, testOpts(80)); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrintTrim(t *testing.T) {
	tests := []struct {
		name string
		doc  func(a *Arena) Doc
		want string
	}{
		{"trim removes trailing spaces", func(a *Arena) Doc { return a.Concat(a.Text("a  "), TrimDoc, a.Text("b")) }, "ab"},
		{"trim removes trailing tabs", func(a *Arena) Doc { return a.Concat(a.Text("a\t\t"), TrimDoc, a.Text("b")) }, "ab"},
		{"trim without trailing whitespace", func(a *Arena) Doc { return a.Concat(a.Text("a"), TrimDoc, a.Text("b")) }, "ab"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := printT(t, tt.doc, testOpts(80)); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrintRemeasure(t *testing.T) {
	// A hard line printed inside a flat-measured group invalidates the
	// measurement; the next group must remeasure instead of trusting the
	// flat shortcut.
	build := func(a *Arena) Doc {
		return a.Group(a.Concat(
			a.Text("a"), HardLineNoBreak,
			a.Group(a.Concat(a.Text("bbbb"), Line, a.Text("cc"))),
			Line,
			a.Text("dd"),
		))
	}

	got := printT(t, build, testOpts(8))

	want := "a\nbbbb\ncc dd"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPrintNewLineOption(t *testing.T) {
	o := Options{PrintWidth: 2, Indent: "  ", TabWidth: 2, NewLine: "\r\n"}

	doc := printT(t, func(a *Arena) Doc { return a.GroupBreak(a.Concat(a.Text("a"), Line, a.Text("b"))) }, o)
	if doc != "a\r\nb" {
		t.Errorf("got %q, want %q", doc, "a\r\nb")
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
			if _, err := Print(Text("x"), tt.opts); err == nil {
				t.Error("expected validation error")
			}
		})
	}
}

func TestPrintUnicodeWidth(t *testing.T) {
	tests := []struct {
		name  string
		doc   func(a *Arena) Doc
		width int
		want  string
	}{
		{
			name:  "wide characters count as two columns",
			doc:   func(a *Arena) Doc { return a.Group(a.Concat(a.Text("日本語"), Line, a.Text("x"))) },
			width: 8,
			want:  "日本語 x",
		},
		{
			name:  "wide characters break the group",
			doc:   func(a *Arena) Doc { return a.Group(a.Concat(a.Text("日本語"), Line, a.Text("x"))) },
			width: 7,
			want:  "日本語\nx",
		},
		{
			name:  "combining marks are zero width",
			doc:   func(a *Arena) Doc { return a.Group(a.Concat(a.Text("e\u0301"), Line, a.Text("x"))) },
			width: 3,
			want:  "e\u0301 x",
		},
		{
			name:  "emoji count as two columns",
			doc:   func(a *Arena) Doc { return a.Group(a.Concat(a.Text("😀"), Line, a.Text("x"))) },
			width: 4,
			want:  "😀 x",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := printT(t, tt.doc, testOpts(tt.width)); got != tt.want {
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
	builds := []func(a *Arena) Doc{
		func(a *Arena) Doc { return a.Group(a.Concat(a.Text("a"), Line, a.Text("b"))) },
		func(a *Arena) Doc {
			return a.Group(a.Concat(a.Text("a"), HardLine, a.Text("b"), SoftLine, a.Text("c")))
		},
		func(a *Arena) Doc {
			return a.ConditionalGroup(0,
				a.Concat(a.Text("a"), Line, a.Text("b"), Line, a.Text("c")),
				a.Concat(a.Text("a"), Line, a.Text("b"), HardLineNoBreak, a.Text("c")),
			)
		},
	}
	for _, build := range builds {
		var a Arena

		d := build(&a)

		first, err := Print(d, testOpts(2))
		if err != nil {
			t.Fatalf("Print: %v", err)
		}

		second, err := Print(d, testOpts(2))
		if err != nil {
			t.Fatalf("Print: %v", err)
		}

		if first != second {
			t.Errorf("not idempotent: %q vs %q", first, second)
		}
	}
}

func TestDocMutability(t *testing.T) {
	// Print mutates the doc (break propagation); printing a fresh doc each
	// time must yield stable output. The arena is reset between prints, so
	// the fresh doc reuses the arena's regions.
	var a Arena

	build := func() Doc {
		a.Reset()

		return a.Group(a.Concat(a.Text("a"), HardLine, a.Text("b"), SoftLine, a.Text("c")))
	}

	first := printT(t, func(*Arena) Doc { return build() }, testOpts(80))
	for i := range 10 {
		if got := printT(t, func(*Arena) Doc { return build() }, testOpts(80)); got != first {
			t.Fatalf("iteration %d: got %q, want %q", i, got, first)
		}
	}
}
