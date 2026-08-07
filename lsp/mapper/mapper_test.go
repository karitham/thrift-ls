package mapper

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/types"
	"github.com/karitham/thrift-ls/syntax"
)

func TestMapper_LSPPosToParserPosition(t *testing.T) {
	type fields struct {
		fileURI uri.URI
		content []byte
	}

	type args struct {
		pos types.Position
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
				fileURI: "test/test.thrift",
				content: []byte(content),
			},
			args: args{
				pos: types.Position{
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
				fileURI: "test/test.thrift",
				content: []byte(content),
			},
			args: args{
				pos: types.Position{
					Line:      3,
					Character: 5,
				},
			},
			want:      syntax.InvalidPosition,
			assertion: assert.Error,
		},
		{
			name: "ascii character exceeded",
			fields: fields{
				fileURI: "test/test.thrift",
				content: []byte(content),
			},
			args: args{
				pos: types.Position{
					Line:      1,
					Character: 28,
				},
			},
			want:      syntax.InvalidPosition,
			assertion: assert.Error,
		},
		{
			name: "ascii character no exceeded end of file",
			fields: fields{
				fileURI: "test/test.thrift",
				content: []byte(content),
			},
			args: args{
				pos: types.Position{
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
			name: "ascii character exceeded end of file",
			fields: fields{
				fileURI: "test/test.thrift",
				content: []byte(content),
			},
			args: args{
				pos: types.Position{
					Line:      2,
					Character: 2,
				},
			},
			want:      syntax.InvalidPosition,
			assertion: assert.Error,
		},
		{
			name: "rune",
			fields: fields{
				fileURI: "test/test.thrift",
				content: []byte(runeContent),
			},
			args: args{
				pos: types.Position{
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
			name: "rune line exceeded",
			fields: fields{
				fileURI: "test/test.thrift",
				content: []byte(content),
			},
			args: args{
				pos: types.Position{
					Line:      2,
					Character: 12,
				},
			},
			want:      syntax.InvalidPosition,
			assertion: assert.Error,
		},
		{
			name: "rune character exceeded",
			fields: fields{
				fileURI: "test/test.thrift",
				content: []byte(content),
			},
			args: args{
				pos: types.Position{
					Line:      0,
					Character: 15,
				},
			},
			want:      syntax.InvalidPosition,
			assertion: assert.Error,
		},
	}
	for i := range tests {
		tt := tests[i]
		t.Run(tt.name, func(t *testing.T) {
			m := &Mapper{
				fileURI: tt.fields.fileURI,
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
		want    types.Position
	}{
		{
			name:    "single line",
			content: "struct Gundam {}",
			want:    types.Position{Line: 0, Character: 16},
		},
		{
			name:    "multiline without trailing newline",
			content: "enum ZeonForces {\n  ZAKU_I\n}",
			want:    types.Position{Line: 2, Character: 1},
		},
		{
			name:    "trailing newline has an empty last line",
			content: "struct Gundam {\n}\n",
			want:    types.Position{Line: 2, Character: 0},
		},
		{
			name:    "non-ascii on the last line",
			content: `const string s = "モビルスーツ"`,
			want:    types.Position{Line: 0, Character: 25},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewMapper("file:///tmp/test.thrift", []byte(tt.content))
			assert.Equal(t, tt.want, m.GetLSPEndPosition())
		})
	}
}
