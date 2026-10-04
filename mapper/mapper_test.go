package mapper

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/syntax"
)

func TestMapper_LSPPosToParserPosition(t *testing.T) {
	type fields struct {
		content []byte
	}

	type args struct {
		pos protocol.Position
	}

	content := `struct demo {
  1: required string name,
}`

	runeContent := `struct 😀😂 {
  1: required string name,
}`

	tests := []struct {
		name      string
		fields    fields
		args      args
		want      syntax.Position
		assertion assert.ErrorAssertionFunc
	}{
		{
			name: "ascii",
			fields: fields{
				content: []byte(content),
			},
			args: args{
				pos: protocol.Position{
					Line:      1,
					Character: 5,
				},
			},
			want: syntax.Position{
				Line:   2,
				Col:    6,
				Offset: 19,
			},
			assertion: assert.NoError,
		},
		{
			name: "ascii line exceeded",
			fields: fields{
				content: []byte(content),
			},
			args: args{
				pos: protocol.Position{
					Line:      3,
					Character: 5,
				},
			},
			want:      syntax.InvalidPosition,
			assertion: assert.Error,
		},
		{
			name: "ascii character clamps to line end",
			fields: fields{
				content: []byte(content),
			},
			args: args{
				pos: protocol.Position{
					Line:      1,
					Character: 28,
				},
			},
			want: syntax.Position{
				Line:   2,
				Col:    27,
				Offset: 40,
			},
			assertion: assert.NoError,
		},
		{
			name: "ascii character no exceeded end of file",
			fields: fields{
				content: []byte(content),
			},
			args: args{
				pos: protocol.Position{
					Line:      2,
					Character: 1,
				},
			},
			want: syntax.Position{
				Line:   3,
				Col:    2,
				Offset: 42,
			},
			assertion: assert.NoError,
		},
		{
			name: "ascii character past EOF clamps",
			fields: fields{
				content: []byte(content),
			},
			args: args{
				pos: protocol.Position{
					Line:      2,
					Character: 2,
				},
			},
			want: syntax.Position{
				Line:   3,
				Col:    2,
				Offset: 42,
			},
			assertion: assert.NoError,
		},
		{
			name: "rune",
			fields: fields{
				content: []byte(runeContent),
			},
			args: args{
				pos: protocol.Position{
					Line:      0,
					Character: 12,
				},
			},
			want: syntax.Position{
				Line:   1,
				Col:    11,
				Offset: 16,
			},
			assertion: assert.NoError,
		},
		{
			name: "rune document short line clamps",
			fields: fields{
				content: []byte(runeContent),
			},
			args: args{
				pos: protocol.Position{
					Line:      2,
					Character: 12,
				},
			},
			want: syntax.Position{
				Line:   3,
				Col:    2,
				Offset: 46,
			},
			assertion: assert.NoError,
		},
		{
			name: "ascii line in rune document clamps",
			fields: fields{
				content: []byte(runeContent),
			},
			args: args{
				pos: protocol.Position{
					Line:      1,
					Character: 99,
				},
			},
			want: syntax.Position{
				Line:   2,
				Col:    27,
				Offset: 44,
			},
			assertion: assert.NoError,
		},
		{
			name: "rune character clamps to line end",
			fields: fields{
				content: []byte(runeContent),
			},
			args: args{
				pos: protocol.Position{
					Line:      0,
					Character: 15,
				},
			},
			want: syntax.Position{
				Line:   1,
				Col:    12,
				Offset: 17,
			},
			assertion: assert.NoError,
		},
	}
	for i := range tests {
		tt := tests[i]
		t.Run(tt.name, func(t *testing.T) {
			m := &Mapper{
				content: tt.fields.content,
			}
			got, err := m.LSPPosToParserPosition(tt.args.pos)
			tt.assertion(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_utf16Count(t *testing.T) {
	type args struct {
		contents []byte
	}

	tests := []struct {
		name string
		args args
		want int
	}{
		{
			name: "normal",
			args: args{
				contents: []byte("aaaaa"),
			},
			want: 5,
		},
		{
			name: "case 2",
			args: args{
				contents: []byte("a😀a😂a"),
			},
			want: 7,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, utf16Count(tt.args.contents))
		})
	}
}

func TestGetLSPEndPosition(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    protocol.Position
	}{
		{
			name:    "empty document",
			content: "",
			want:    protocol.Position{Line: 0, Character: 0},
		},
		{
			name:    "single line",
			content: "struct Gundam {}",
			want:    protocol.Position{Line: 0, Character: 16},
		},
		{
			name:    "multiline without trailing newline",
			content: "enum ZeonForces {\n  ZAKU_I\n}",
			want:    protocol.Position{Line: 2, Character: 1},
		},
		{
			name:    "trailing newline has an empty last line",
			content: "struct Gundam {\n}\n",
			want:    protocol.Position{Line: 2, Character: 0},
		},
		{
			name:    "non-ascii on the last line",
			content: `const string s = "モビルスーツ"`,
			want:    protocol.Position{Line: 0, Character: 25},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewMapper([]byte(tt.content))
			assert.Equal(t, tt.want, m.GetLSPEndPosition())
		})
	}
}

// TestMapper_LSPPosToParserPositionStaleColumn pins that a column past the
// line content clamps to the content end before the line terminator.
func TestMapper_LSPPosToParserPositionStaleColumn(t *testing.T) {
	tests := []struct {
		name    string
		content string
		pos     protocol.Position
		want    syntax.Position
	}{
		{
			name:    "stale column stops before newline",
			content: "abc\ndef\n",
			pos:     protocol.Position{Line: 0, Character: 99},
			want:    syntax.Position{Line: 1, Col: 4, Offset: 3},
		},
		{
			name:    "stale column stops before crlf",
			content: "ab\r\ncd",
			pos:     protocol.Position{Line: 0, Character: 99},
			want:    syntax.Position{Line: 1, Col: 3, Offset: 2},
		},
		{
			name:    "stale column in unicode line stops before crlf",
			content: "héllo\r\nworld",
			pos:     protocol.Position{Line: 0, Character: 99},
			want:    syntax.Position{Line: 1, Col: 6, Offset: 6},
		},
		{
			name:    "stale column on empty line",
			content: "\nstruct A {}\n",
			pos:     protocol.Position{Line: 0, Character: 99},
			want:    syntax.Position{Line: 1, Col: 1, Offset: 0},
		},
		{
			name:    "stale column on middle line",
			content: "one\ntwo\n",
			pos:     protocol.Position{Line: 1, Character: 99},
			want:    syntax.Position{Line: 2, Col: 4, Offset: 7},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewMapper([]byte(tt.content))
			got, err := m.LSPPosToParserPosition(tt.pos)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
