package source

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBlockDiff(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
		want []blockEdit
	}{
		{
			name: "unchanged documents yield no edits",
			old:  "struct A {}\n\nstruct B {}\n",
			new:  "struct A {}\n\nstruct B {}\n",
			want: nil,
		},
		{
			name: "one changed block",
			old:  "struct A {}\n\nstruct B {\n2: i32 b\n}\n",
			new:  "struct A {}\n\nstruct B { 2: i32 b }\n",
			want: []blockEdit{{
				start: strings.Index("struct A {}\n\nstruct B {\n2: i32 b\n}\n", "struct B"),
				end:   len("struct A {}\n\nstruct B {\n2: i32 b\n}\n"),
				text:  "struct B { 2: i32 b }\n",
			}},
		},
		{
			name: "two changed blocks get separate edits",
			old:  "struct A {\n1: string a\n}\n\nstruct B {\n2: i32 b\n}\n",
			new:  "struct A { 1: string a }\n\nstruct B { 2: i32 b }\n",
			want: []blockEdit{
				{0, len("struct A {\n1: string a\n}\n"), "struct A { 1: string a }\n"},
				{len("struct A {\n1: string a\n}\n\n"), len("struct A {\n1: string a\n}\n\nstruct B {\n2: i32 b\n}\n"), "struct B { 2: i32 b }\n"},
			},
		},
		{
			name: "leading blank lines are dropped",
			old:  "\n\nstruct A {}\n",
			new:  "struct A {}\n",
			want: []blockEdit{{
				start: 0,
				end:   strings.Index("\n\nstruct A {}\n", "struct"),
				text:  "",
			}},
		},
		{
			name: "trailing blank lines are dropped",
			old:  "struct A {}\n\n\n",
			new:  "struct A {}\n",
			want: []blockEdit{{
				start: len("struct A {}\n"),
				end:   len("struct A {}\n\n\n"),
				text:  "",
			}},
		},
		{
			name: "crlf input falls back to a whole-document edit",
			old:  "struct A {}\r\n\r\nstruct B {\r\n2: i32 b\r\n}\r\n",
			new:  "struct A {}\r\n\r\nstruct B { 2: i32 b }\r\n",
			want: []blockEdit{{0, len("struct A {}\r\n\r\nstruct B {\r\n2: i32 b\r\n}\r\n"), "struct A {}\r\n\r\nstruct B { 2: i32 b }\r\n"}},
		},
		{
			name: "unaligned block structure falls back to one edit",
			old:  "struct A {}\n\n\nstruct B {}\n",
			new:  "struct A {}\n\nstruct B {}\n\nstruct C {}\n",
			want: []blockEdit{{0, len("struct A {}\n\n\nstruct B {}\n"), "struct A {}\n\nstruct B {}\n\nstruct C {}\n"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := blockDiff([]byte(tt.old), []byte(tt.new))
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestBlockDiffApplyAll pins the core invariant: applying every edit
// reproduces the new document.
func TestBlockDiffApplyAll(t *testing.T) {
	old := "struct A {\n1: string a\n}\n\nstruct B {\n2: i32 b\n}\n\nstruct C {\n3: i64 c\n}\n"
	new := "struct A { 1: string a }\n\nstruct B { 2: i32 b }\n\nstruct C { 3: i64 c }\n"

	edits := blockDiff([]byte(old), []byte(new))

	var sb strings.Builder

	prev := 0
	for _, e := range edits {
		require.GreaterOrEqual(t, e.start, prev, "edits must be ordered and non-overlapping")
		sb.WriteString(old[prev:e.start])
		sb.WriteString(e.text)
		prev = e.end
	}

	sb.WriteString(old[prev:])

	assert.Equal(t, new, sb.String())
}

// TestBlockDiffSpliceSafety pins that every edit is bounded by blank lines
// or file edges: splicing it alone never merges with neighboring content.
func TestBlockDiffSpliceSafety(t *testing.T) {
	old := "struct A {}\n\nstruct B {\n2: i32 b\n}\n\nstruct C {}\n"
	new := "struct A {}\n\nstruct B { 2: i32 b }\n\nstruct C {}\n"

	edits := blockDiff([]byte(old), []byte(new))
	require.Len(t, edits, 1)

	e := edits[0]
	if e.start != 0 {
		assert.Equal(t, byte('\n'), old[e.start-1], "edit must start after a blank line")
	}

	if e.end != len(old) {
		assert.Equal(t, byte('\n'), old[e.end], "edit must end before a blank line")
	}
}
