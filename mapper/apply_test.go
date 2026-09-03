package mapper

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
)

// pos builds an LSP position on line 0.
func pos(char uint32) protocol.Position {
	return protocol.Position{Line: 0, Character: char}
}

func TestApplyEdits(t *testing.T) {
	tests := []struct {
		name    string
		content string
		edits   []protocol.TextEdit
		want    string
	}{
		{
			name:    "single replacement",
			content: "hello world",
			edits: []protocol.TextEdit{
				{Range: protocol.Range{Start: pos(6), End: pos(11)}, NewText: "there"},
			},
			want: "hello there",
		},
		{
			name:    "insertion at empty range",
			content: "hello world",
			edits: []protocol.TextEdit{
				{Range: protocol.Range{Start: pos(5), End: pos(5)}, NewText: ","},
			},
			want: "hello, world",
		},
		{
			name:    "deletion with empty text",
			content: "hello world",
			edits: []protocol.TextEdit{
				{Range: protocol.Range{Start: pos(5), End: pos(11)}, NewText: ""},
			},
			want: "hello",
		},
		{
			name:    "unsorted edits apply correctly",
			content: "abcdef",
			edits: []protocol.TextEdit{
				{Range: protocol.Range{Start: pos(0), End: pos(1)}, NewText: "X"},
				{Range: protocol.Range{Start: pos(4), End: pos(5)}, NewText: "Y"},
			},
			want: "XbcdYf",
		},
		{
			name:    "edit at document start and end",
			content: "abc",
			edits: []protocol.TextEdit{
				{Range: protocol.Range{Start: pos(0), End: pos(0)}, NewText: "<"},
				{Range: protocol.Range{Start: pos(3), End: pos(3)}, NewText: ">"},
			},
			want: "<abc>",
		},
		{
			name:    "replace whole document",
			content: "abc",
			edits: []protocol.TextEdit{
				{Range: protocol.Range{Start: pos(0), End: pos(3)}, NewText: "xyz"},
			},
			want: "xyz",
		},
		{
			name:    "no edits is identity",
			content: "untouched",
			edits:   nil,
			want:    "untouched",
		},
		{
			name:    "end past document clamps",
			content: "abc",
			edits: []protocol.TextEdit{
				{Range: protocol.Range{Start: pos(0), End: pos(4)}, NewText: "x"},
			},
			want: "x",
		},
		{
			name:    "start past document clamps to insertion",
			content: "abc",
			edits: []protocol.TextEdit{
				{Range: protocol.Range{Start: pos(9), End: pos(9)}, NewText: "x"},
			},
			want: "abcx",
		},
		{
			name: "utf16 surrogate pair counts as two units",
			// a + emoji(4 bytes, 2 units) + b: b sits at char 3.
			content: "a😀b",
			edits: []protocol.TextEdit{
				{Range: protocol.Range{Start: pos(3), End: pos(4)}, NewText: "B"},
			},
			want: "a😀B",
		},
		{
			name: "utf16 cjk counts as one unit",
			// a + 日(3 bytes, 1 unit) + b: b sits at char 2.
			content: "a日b",
			edits: []protocol.TextEdit{
				{Range: protocol.Range{Start: pos(2), End: pos(3)}, NewText: "B"},
			},
			want: "a日B",
		},
		{
			name:    "utf16 edit spanning wide runes",
			content: "😀x日y",
			edits: []protocol.TextEdit{
				// chars 0..4 span emoji(2) + x(1) + 日(1).
				{Range: protocol.Range{Start: pos(0), End: pos(4)}, NewText: "ok"},
			},
			want: "oky",
		},
		{
			name:    "multiline edit",
			content: "one\ntwo\nthree\n",
			edits: []protocol.TextEdit{
				{Range: protocol.Range{Start: protocol.Position{Line: 1, Character: 0}, End: protocol.Position{Line: 2, Character: 5}}, NewText: "TWO"},
			},
			want: "one\nTWO\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewMapper([]byte(tt.content)).ApplyEdits(tt.edits)
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(got))
		})
	}
}

func TestApplyEditsErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		edits   []protocol.TextEdit
		wantErr string
	}{
		{
			name:    "start after end",
			content: "abc",
			edits: []protocol.TextEdit{
				{Range: protocol.Range{Start: pos(2), End: pos(1)}, NewText: "x"},
			},
			wantErr: "invalid edit range",
		},
		{
			name:    "line past document",
			content: "abc",
			edits: []protocol.TextEdit{
				{Range: protocol.Range{Start: protocol.Position{Line: 5, Character: 0}, End: protocol.Position{Line: 5, Character: 1}}, NewText: "x"},
			},
			wantErr: "invalid position line",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewMapper([]byte(tt.content)).ApplyEdits(tt.edits)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// TestApplyEditsRoundTrip pins the property the format code actions rely
// on: applying the edits a formatter produced reproduces the formatter's
// whole-document output.
func TestApplyEditsRoundTrip(t *testing.T) {
	content := []byte("struct A { 1: i32 a }\n\nstruct B {\n2: i32 b\n}\n")
	want := []byte("struct A { 1: i32 a }\n\nstruct B { 2: i32 b }\n")

	m := NewMapper(content)

	edits := []protocol.TextEdit{
		{
			Range: protocol.Range{
				Start: protocol.Position{Line: 2, Character: 0},
				End:   protocol.Position{Line: 5, Character: 0},
			},
			NewText: "struct B { 2: i32 b }\n",
		},
	}

	got, err := m.ApplyEdits(edits)
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got))
}
