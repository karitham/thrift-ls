package lsp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/karitham/thrift-ls/formatter"
	"github.com/karitham/thrift-ls/syntax"
)

func TestFormatRangeText(t *testing.T) {
	opts := formatter.Options{}

	tests := []struct {
		name    string
		content string
		sel     func(content string) (rs, re int)
		want    string
		wantOK  bool
	}{
		{
			// selection inside the struct B line: expands to the whole line,
			// bounded by the blank lines around struct B.
			name:    "formats struct bounded by blank lines",
			content: "struct A {\n1: string a\n}\n\nstruct B {  1: i32 b }",
			sel: func(c string) (int, int) {
				return strings.Index(c, "  1:") + 1, strings.Index(c, "i32") + 1
			},
			want:   "struct B { 1: i32 b }",
			wantOK: true,
		},
		{
			// selection covering both declarations, bounded by blank lines.
			name:    "selection of two structs formats both",
			content: "\nstruct A {\n1: string a\n}\n\nstruct B {\n2: i32 b\n}\n\nstruct C {}",
			sel: func(c string) (int, int) {
				return strings.Index(c, "struct A"), strings.Index(c, "struct C") - 2
			},
			want:   "struct A { 1: string a }\n\nstruct B { 2: i32 b }",
			wantOK: true,
		},
		{
			// selection starts on a blank line before struct B.
			name:    "trims leading blank lines from selection",
			content: "struct A {}\n\n\n\nstruct B {\n1: string b\n}",
			sel: func(c string) (int, int) {
				return strings.Index(c, "struct B"), len(c)
			},
			want:   "struct B { 1: string b }",
			wantOK: true,
		},
		{
			// selection ends on a blank line after struct B.
			name:    "trims trailing blank lines from selection",
			content: "struct A {}\n\nstruct B {\n1: string b\n}\n\n\nstruct C {}",
			sel: func(c string) (int, int) {
				return strings.Index(c, "struct B"), strings.Index(c, "struct C") - 2
			},
			want:   "struct B { 1: string b }",
			wantOK: true,
		},
		{
			// selection cuts struct B in half: the expanded slice does not
			// parse, so formatting is refused.
			name:    "refuses when selection cuts a declaration",
			content: "struct A {}\n\nstruct B {\n1: string b\n}\n\nstruct C {}",
			sel: func(c string) (int, int) {
				return strings.Index(c, "struct B") + 6, strings.Index(c, "1: string b") + 4
			},
			wantOK: false,
		},
		{
			// the line right after the range is not blank.
			name:    "refuses when next line is not blank",
			content: "struct A {\n1: string a\n}\nstruct B {}",
			sel: func(c string) (int, int) {
				return 0, strings.Index(c, "struct B") - 1
			},
			wantOK: false,
		},
		{
			// slice is "struct B {\n1: string b" — unclosed, does not parse.
			name:    "refuses when slice does not parse",
			content: "\nstruct B {\n1: string b\n\nstruct C {}",
			sel: func(c string) (int, int) {
				return strings.Index(c, "struct B"), strings.Index(c, "1: string b") + 3
			},
			wantOK: false,
		},
		{
			name:    "refuses whitespace-only selection",
			content: "struct A {}\n\n\nstruct B {}",
			sel: func(c string) (int, int) {
				return strings.Index(c, "struct B") - 2, strings.Index(c, "struct B") - 1
			},
			wantOK: false,
		},
		{
			name:    "refuses empty selection",
			content: "struct A {}",
			sel:     func(c string) (int, int) { return 5, 5 },
			wantOK:  false,
		},
		{
			name:    "handles CRLF line endings",
			content: "struct A {}\r\n\r\nstruct B {\r\n1: string b\r\n}\r\n\r\nstruct C {}",
			sel: func(c string) (int, int) {
				return strings.Index(c, "struct B") + 3, strings.Index(c, "struct C") - 4
			},
			want:   "struct B { 1: string b }",
			wantOK: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rs, re := tt.sel(tt.content)
			got, _, _, ok := formatRangeText([]byte(tt.content), rs, re, opts)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestFormatRangeTextSplice verifies that applying the formatted slice to the
// original content reproduces a whole-document formatting.
func TestFormatRangeTextSplice(t *testing.T) {
	opts := formatter.Options{}
	content := "struct A { 1: string a }\n\nstruct B {  1: i32 b  }\n\nstruct C { 3: i64 c }\n"

	rs := strings.Index(content, "struct B")
	re := rs + len("struct B {  ") // inside struct B's line

	newText, gotRS, gotRE, ok := formatRangeText([]byte(content), rs, re, opts)
	if !ok {
		t.Fatalf("formatRangeText refused a valid range")
	}

	// The returned range must cover the full struct B line, not the raw
	// mid-line selection: it starts at the line start and ends at the
	// line's newline.
	if gotRS != strings.Index(content, "struct B") {
		t.Errorf("effective start = %d, want %d", gotRS, strings.Index(content, "struct B"))
	}

	if got := content[gotRE]; got != '\n' {
		t.Errorf("effective end = %d, want a newline position (got %q)", gotRE, got)
	}

	spliced := content[:gotRS] + newText + content[gotRE:]

	doc, errs := syntax.Parse([]byte(content))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}

	want, err := formatter.Format(doc, opts)
	if err != nil {
		t.Fatalf("whole-doc format: %v", err)
	}

	if spliced != want {
		t.Errorf("splice mismatch\n got: %q\nwant: %q", spliced, want)
	}
}
