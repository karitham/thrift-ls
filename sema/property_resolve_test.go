package sema_test

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/sema"
	"github.com/karitham/thrift-ls/store"
	"github.com/karitham/thrift-ls/syntax"
)

// mdlRoot is the directory every generated workspace lives in.
const mdlRoot = "/w"

// mdlNewView builds an in-memory view over files and parses every file.
func mdlNewView(files map[uri.URI][]byte) *store.View {
	v := store.NewView(uri.File(mdlRoot), store.NewOverlayFS(store.NewMemFS(files)), nil)

	for _, u := range slices.Sorted(maps.Keys(files)) {
		_, _ = v.Parse(context.Background(), u)
	}

	return v
}

// mdlFieldType wraps a name in the identifier type the index resolves.
func mdlFieldType(text string) *syntax.FieldType {
	return &syntax.FieldType{Kind: syntax.TypeIdent, Ident: &syntax.Identifier{Text: text}}
}

// mdlDescOf renders a production FieldType for comparison.
func mdlDescOf(ft *syntax.FieldType) string {
	if ft == nil {
		return "<nil>"
	}

	switch ft.Kind {
	case syntax.TypeIdent:
		if ft.Ident == nil {
			return "ident(?)"
		}

		return "ident(" + ft.Ident.Text + ")"
	case syntax.TypeBase:
		return "base(" + ft.Base.String() + ")"
	case syntax.TypeMap:
		return "map"
	case syntax.TypeList:
		return "list"
	case syntax.TypeSet:
		return "set"
	}

	return fmt.Sprintf("kind(%d)", ft.Kind)
}

func TestUnderlyingType(t *testing.T) {
	a := uri.File("/w/a.thrift")
	b := uri.File("/w/b.thrift")

	for _, tt := range []struct {
		name      string
		files     map[uri.URI][]byte
		query     string
		want      string
		wantScope uri.URI
	}{
		{
			name:  "same file cycle",
			files: map[uri.URI][]byte{a: []byte("typedef B A\ntypedef A B\n")},
			query: "A",
			want:  "<nil>",
		},
		{
			name: "cross file cycle",
			files: map[uri.URI][]byte{
				a: []byte(`include "b.thrift"
typedef b.B A
typedef B A2
`),
				b: []byte(`include "a.thrift"
typedef a.A2 B
typedef B A3
`),
			},
			query: "A",
			want:  "<nil>",
		},
		{
			name:      "chain to base type",
			files:     map[uri.URI][]byte{a: []byte("typedef i32 Age\ntypedef Age Years\n")},
			query:     "Years",
			want:      "base(i32)",
			wantScope: a,
		},
		{
			name:      "chain to container",
			files:     map[uri.URI][]byte{a: []byte("typedef i32 Age\ntypedef list<Age> Ages\n")},
			query:     "Ages",
			want:      "list",
			wantScope: a,
		},
		{
			name: "chain through include",
			files: map[uri.URI][]byte{
				a: []byte(`include "b.thrift"
typedef b.Other Mine
`),
				b: []byte("typedef string Other\n"),
			},
			query:     "Mine",
			want:      "base(string)",
			wantScope: b,
		},
		{
			name:  "unresolved identifier",
			files: map[uri.URI][]byte{a: []byte("typedef Ghost A\n")},
			query: "A",
			want:  "<nil>",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			view := mdlNewView(tt.files)

			pf, err := view.Parse(ctx, a)
			require.NoError(t, err)

			got, scope := sema.NewIndex(view).UnderlyingType(ctx, pf, mdlFieldType(tt.query))
			assert.Equal(t, tt.want, mdlDescOf(got))

			if tt.wantScope != "" {
				require.NotNil(t, scope)
				assert.Equal(t, tt.wantScope, scope.URI())
			}
		})
	}
}
