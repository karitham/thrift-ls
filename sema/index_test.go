package sema

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/store"
	"github.com/karitham/thrift-ls/syntax"
)

func TestIndex_ResolveType_SameFile(t *testing.T) {
	ctx := t.Context()
	view := store.BuildViewForTest([]*store.FileChange{{URI: uri.File("/t.thrift"), Content: []byte("struct Foo {}\ntypedef i32 Age"), From: store.FileChangeTypeDidOpen}})
	from := parseOne(t, view, uri.File("/t.thrift"))

	def, err := NewIndex(view).ResolveType(ctx, from, ft("Foo"))
	require.NoError(t, err)
	require.NotNil(t, def)
	assert.Equal(t, uri.File("/t.thrift"), def.File)
	assert.Equal(t, DefinitionStruct, def.Kind)

	def2, err := NewIndex(view).ResolveType(ctx, from, ft("Age"))
	require.NoError(t, err)
	require.NotNil(t, def2)
	assert.Equal(t, DefinitionTypedef, def2.Kind)

	def3, err := NewIndex(view).ResolveType(ctx, from, ft("i32"))
	require.NoError(t, err)
	assert.Nil(t, def3)
}

func TestIndex_ResolveType_IncludeChain(t *testing.T) {
	ctx := t.Context()
	view := crossSnap(t, "/a.thrift", `include "b.thrift"
struct Foo { 1: b.Bar bar, }`, "/b.thrift", "struct Bar {}")
	a := parseOne(t, view, uri.File("/a.thrift"))

	def, err := NewIndex(view).ResolveType(ctx, a, ft("b.Bar"))
	require.NoError(t, err)
	require.NotNil(t, def)
	assert.Equal(t, uri.File("/b.thrift"), def.File)
	assert.Equal(t, "Bar", def.Name.Text)
	assert.Equal(t, DefinitionStruct, def.Kind)

	ghost, err := NewIndex(view).ResolveType(ctx, a, ft("b.Ghost"))
	require.NoError(t, err)
	assert.Nil(t, ghost, "unknown names resolve to nothing")
}

func TestIndex_ResolveValue(t *testing.T) {
	ctx := t.Context()
	view := crossSnap(t, "/a.thrift", `include "b.thrift"
const i32 C = b.MAX`, "/b.thrift", "const i32 MAX = 10\nenum Color { RED }")
	a := parseOne(t, view, uri.File("/a.thrift"))

	def, err := NewIndex(view).ResolveValue(ctx, a, cv("b.MAX"))
	require.NoError(t, err)
	require.NotNil(t, def)
	assert.Equal(t, uri.File("/b.thrift"), def.File)
	assert.Equal(t, DefinitionConst, def.Kind)

	// RED is defined in b.thrift, resolved through the include chain.
	def2, err := NewIndex(view).ResolveValue(ctx, a, cv("RED"))
	require.NoError(t, err)
	require.NotNil(t, def2)
	assert.Equal(t, uri.File("/b.thrift"), def2.File)
	assert.Equal(t, DefinitionEnumValue, def2.Kind)

	def3, err := NewIndex(view).ResolveValue(ctx, a, cv("true"))
	require.NoError(t, err)
	assert.Nil(t, def3)
}

func TestIndex_ResolveService(t *testing.T) {
	ctx := t.Context()
	view := store.BuildViewForTest([]*store.FileChange{{URI: uri.File("/t.thrift"), Content: []byte("service Base {}"), From: store.FileChangeTypeDidOpen}})
	a := parseOne(t, view, uri.File("/t.thrift"))
	def, err := NewIndex(view).ResolveService(ctx, a, &syntax.Identifier{Text: "Base"})
	require.NoError(t, err)
	require.NotNil(t, def)
	assert.Equal(t, DefinitionService, def.Kind)

	ghost, err := NewIndex(view).ResolveService(ctx, a, &syntax.Identifier{Text: "Ghost"})
	require.NoError(t, err)
	assert.Nil(t, ghost, "unknown services resolve to nothing")
}

func TestIndex_References_Type(t *testing.T) {
	ctx := t.Context()
	view := store.BuildViewForTest([]*store.FileChange{{URI: uri.File("/t.thrift"), Content: []byte("struct User {}\nstruct Foo { 1: User user, 2: list<User> users, }\nservice Svc { User get(1: i32 id); }"), From: store.FileChangeTypeDidOpen}})
	_ = parseOne(t, view, uri.File("/t.thrift"))

	hits, err := NewIndex(view).References(ctx, uri.File("/t.thrift"), "User", store.RefFieldType, store.RefSignatureType)
	require.NoError(t, err)
	require.Len(t, hits, 3)

	ghosts, err := NewIndex(view).References(ctx, uri.File("/t.thrift"), "Ghost", store.RefFieldType, store.RefSignatureType)
	require.NoError(t, err)
	assert.Empty(t, ghosts, "unknown names have no references")

	wrongKind, err := NewIndex(view).References(ctx, uri.File("/t.thrift"), "User", store.RefConstValue)
	require.NoError(t, err)
	assert.Empty(t, wrongKind, "value slots hold no type references")
}

func TestIndex_References_ExceptionRule(t *testing.T) {
	ctx := t.Context()
	view := store.BuildViewForTest([]*store.FileChange{{URI: uri.File("/t.thrift"), Content: []byte("exception Bad {}\nstruct Foo { 1: Bad bad, }\nservice Svc { void f() throws (1: Bad e); }"), From: store.FileChangeTypeDidOpen}})
	_ = parseOne(t, view, uri.File("/t.thrift"))

	hits, err := NewIndex(view).References(ctx, uri.File("/t.thrift"), "Bad", store.RefSignatureType)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "Bad", hits[0].Text)

	fieldHits, err := NewIndex(view).References(ctx, uri.File("/t.thrift"), "Bad", store.RefFieldType)
	require.NoError(t, err)
	require.Len(t, fieldHits, 1, "field slots see the same reference")
}

func TestIndex_References_ConstValue(t *testing.T) {
	ctx := t.Context()
	view := store.BuildViewForTest([]*store.FileChange{{URI: uri.File("/t.thrift"), Content: []byte("const i32 MAX = 10\nstruct Foo { 1: i32 id = MAX, }"), From: store.FileChangeTypeDidOpen}})
	_ = parseOne(t, view, uri.File("/t.thrift"))

	hits, err := NewIndex(view).References(ctx, uri.File("/t.thrift"), "MAX", store.RefConstValue)
	require.NoError(t, err)
	require.Len(t, hits, 1)

	typeHits, err := NewIndex(view).References(ctx, uri.File("/t.thrift"), "MAX", store.RefFieldType)
	require.NoError(t, err)
	assert.Empty(t, typeHits, "type slots hold no value references")
}

func TestIndex_ReferencesToEnumValues(t *testing.T) {
	ctx := t.Context()
	view := store.BuildViewForTest([]*store.FileChange{{URI: uri.File("/t.thrift"), Content: []byte("enum Color { RED = 0, BLUE = 1 }\nstruct Foo { 1: i32 id = Color.RED, }\nconst i32 C = Color.BLUE"), From: store.FileChangeTypeDidOpen}})
	pf := parseOne(t, view, uri.File("/t.thrift"))

	def, err := NewIndex(view).ResolveType(ctx, pf, ft("Color"))
	require.NoError(t, err)
	require.NotNil(t, def)

	hits, err := NewIndex(view).ReferencesTo(ctx, def, store.RefFieldType, store.RefSignatureType, store.RefConstValue)
	require.NoError(t, err)
	require.Len(t, hits, 2)
	for _, h := range hits {
		assert.Equal(t, "Color", h.Text)
	}

	ghostHits, err := NewIndex(view).ReferencesTo(ctx, def, store.RefFieldType)
	require.NoError(t, err)
	assert.Empty(t, ghostHits, "the enum is never used as a field type here")
}

func TestIndex_ReferencingFiles(t *testing.T) {
	view := crossSnap(t, "/a.thrift", `include "b.thrift"`, "/b.thrift", "")
	_ = parseOne(t, view, uri.File("/a.thrift"))
	files := NewIndex(view).ReferencingFiles(uri.File("/b.thrift"))
	require.Len(t, files, 1)
	assert.Equal(t, uri.File("/a.thrift"), files[0])
}

func TestIndex_FindInWorkspace(t *testing.T) {
	tests := []struct {
		name  string
		query string
		file  string
	}{
		{name: "unqualified", query: "Account", file: "/b.thrift"},
		{name: "qualified", query: "zeon.Account", file: "/zeon.thrift"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			view := crossSnap(t, "/a.thrift", "struct User {}", tt.file, "struct Account {}")
			_ = parseOne(t, view, uri.File("/a.thrift"))
			_ = parseOne(t, view, uri.File(tt.file))

			def, err := NewIndex(view).FindInWorkspace(ctx, tt.query)
			require.NoError(t, err)
			require.NotNil(t, def)
			assert.Equal(t, uri.File(tt.file), def.File)
			assert.Equal(t, DefinitionStruct, def.Kind)
		})
	}
}

func TestRefKindsFor(t *testing.T) {
	assert.Equal(t, []store.RefKind{store.RefSignatureType, store.RefAnnotationType}, RefKindsFor(DefinitionException))
	assert.Equal(t, []store.RefKind{store.RefFieldType, store.RefSignatureType, store.RefAnnotationType}, RefKindsFor(DefinitionStruct))
	assert.Equal(t, []store.RefKind{store.RefServiceExtends}, RefKindsFor(DefinitionService))
	assert.Equal(t, []store.RefKind{store.RefConstValue}, RefKindsFor(DefinitionConst))
}

// --- helpers ---

// crossSnap builds a snapshot with two files, parsed in dependency order
// (includes first), so the include graph resolves correctly.
func crossSnap(t *testing.T, fa, ca, fb, cb string) *store.View {
	t.Helper()
	view := store.BuildViewForTest([]*store.FileChange{
		{URI: uri.File(fb), Version: 0, Content: []byte(cb), From: store.FileChangeTypeDidOpen},
		{URI: uri.File(fa), Version: 0, Content: []byte(ca), From: store.FileChangeTypeDidOpen},
	})
	return view
}

func parseOne(t *testing.T, view *store.View, u uri.URI) *store.ParsedFile {
	t.Helper()
	pf, err := view.Parse(t.Context(), u)
	require.NoError(t, err)
	return pf
}

func ft(name string) *syntax.FieldType {
	return &syntax.FieldType{Kind: syntax.TypeIdent, Ident: &syntax.Identifier{Text: name}}
}

func cv(s string) *syntax.ConstValue {
	if s == "true" || s == "false" {
		return &syntax.ConstValue{Kind: syntax.ValueInt, Text: s}
	}
	return &syntax.ConstValue{Kind: syntax.ValueIdent, Text: s}
}
