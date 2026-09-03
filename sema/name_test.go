package sema

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/syntax"
)

func TestIncludeNameOf(t *testing.T) {
	type args struct {
		file uri.URI
	}

	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "file name",
			args: args{
				file: uri.MustParse("base.thrift"),
			},
			want: "base",
		},
		{
			name: "file name with dir",
			args: args{
				file: uri.MustParse("/tmp/base.thrift"),
			},
			want: "base",
		},
		{
			name: "file name with .",
			args: args{
				file: uri.MustParse("/tmp/base.subpath.thrift"),
			},
			want: "base.subpath",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IncludeNameOf(tt.args.file))
		})
	}
}

func TestIncludeNames(t *testing.T) {
	cur := uri.MustParse("/tmp/app.thrift")
	includes := []*syntax.Include{
		{Path: &syntax.Token{Text: "../../base.sub.thrift"}},
		{Path: &syntax.Token{Text: "user.sub.thrift"}},
		{Path: &syntax.Token{Text: "app.thrift"}},
		{Path: nil},
	}

	assert.Equal(t,
		[]string{"base.sub", "user.sub", "app"},
		includeNames(cur, includes),
		"nil paths are skipped")

	assert.Empty(t, includeNames(cur, nil), "empty includes yield no names")
}

func TestParseIdent(t *testing.T) {
	type args struct {
		cur        uri.URI
		includes   []*syntax.Include
		identifier string
	}

	tests := []struct {
		name        string
		args        args
		wantInclude string
		wantIdent   string
	}{
		{
			name: "case 1",
			args: args{
				cur: uri.MustParse("/tmp/app.thrift"),
				includes: []*syntax.Include{
					{
						Path: &syntax.Token{Text: "user.sub.thrift"},
					},
					{
						Path: &syntax.Token{Text: "user.thrift"},
					},
				},
				identifier: "user.Name",
			},
			wantInclude: "user",
			wantIdent:   "Name",
		},
		{
			name: "case 2",
			args: args{
				cur: uri.MustParse("/tmp/app.thrift"),
				includes: []*syntax.Include{
					{
						Path: &syntax.Token{Text: "user.sub.thrift"},
					},
					{
						Path: &syntax.Token{Text: "user.thrift"},
					},
				},
				identifier: "user.sub.Name",
			},
			wantInclude: "user.sub",
			wantIdent:   "Name",
		},
		{
			name: "case 3",
			args: args{
				cur: uri.MustParse("/tmp/app.thrift"),
				includes: []*syntax.Include{
					{
						Path: &syntax.Token{Text: "user.thrift"},
					},
				},
				identifier: "user.sub.Name",
			},
			wantInclude: "user",
			wantIdent:   "sub.Name",
		},
		{
			name: "bare identifier has no qualifier",
			args: args{
				cur: uri.MustParse("/tmp/app.thrift"),
				includes: []*syntax.Include{
					{
						Path: &syntax.Token{Text: "user.thrift"},
					},
				},
				identifier: "Name",
			},
			wantInclude: "",
			wantIdent:   "Name",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotInclude, gotIdent := ParseIdent(tt.args.cur, tt.args.includes, tt.args.identifier)
			assert.Equal(t, tt.wantInclude, gotInclude)
			assert.Equal(t, tt.wantIdent, gotIdent)
		})
	}
}
