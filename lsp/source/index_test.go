package source

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

var ctx = context.Background()

func TestIndex_ResolveType_SameFile(t *testing.T) {
	ss := snap(t, "/t.thrift", "struct Foo {}\ntypedef i32 Age")
	from := parseOne(t, ss, fu("/t.thrift"))

	def, err := NewIndex(ss).ResolveType(ctx, from, ft("Foo"))
	require.NoError(t, err)
	require.NotNil(t, def)
	assert.Equal(t, fu("/t.thrift"), def.File)
	assert.Equal(t, DefinitionStruct, def.Kind)

	def2, err := NewIndex(ss).ResolveType(ctx, from, ft("Age"))
	require.NoError(t, err)
	require.NotNil(t, def2)
	assert.Equal(t, DefinitionTypedef, def2.Kind)

	def3, err := NewIndex(ss).ResolveType(ctx, from, ft("i32"))
	require.NoError(t, err)
	assert.Nil(t, def3)
}

func TestIndex_ResolveType_IncludeChain(t *testing.T) {
	ss := crossSnap(t, "/a.thrift", `include "b.thrift"
struct Foo { 1: b.Bar bar, }`, "/b.thrift", "struct Bar {}")
	a := parseOne(t, ss, fu("/a.thrift"))

	def, err := NewIndex(ss).ResolveType(ctx, a, ft("b.Bar"))
	require.NoError(t, err)
	require.NotNil(t, def)
	assert.Equal(t, fu("/b.thrift"), def.File)
	assert.Equal(t, "Bar", def.Name.Text)
	assert.Equal(t, DefinitionStruct, def.Kind)
}

func TestIndex_ResolveValue(t *testing.T) {
	ss := crossSnap(t, "/a.thrift", `include "b.thrift"
const i32 C = b.MAX`, "/b.thrift", "const i32 MAX = 10\nenum Color { RED }")
	a := parseOne(t, ss, fu("/a.thrift"))

	def, err := NewIndex(ss).ResolveValue(ctx, a, cv("b.MAX"))
	require.NoError(t, err)
	require.NotNil(t, def)
	assert.Equal(t, fu("/b.thrift"), def.File)
	assert.Equal(t, DefinitionConst, def.Kind)

	// RED is defined in b.thrift, resolved through the include chain.
	def2, err := NewIndex(ss).ResolveValue(ctx, a, cv("RED"))
	require.NoError(t, err)
	require.NotNil(t, def2)
	assert.Equal(t, fu("/b.thrift"), def2.File)
	assert.Equal(t, DefinitionEnumValue, def2.Kind)

	def3, err := NewIndex(ss).ResolveValue(ctx, a, cv("true"))
	require.NoError(t, err)
	assert.Nil(t, def3)
}

func TestIndex_ResolveService(t *testing.T) {
	ss := snap(t, "/t.thrift", "service Base {}")
	a := parseOne(t, ss, fu("/t.thrift"))
	def, err := NewIndex(ss).ResolveService(ctx, a, &syntax.Identifier{Text: "Base"})
	require.NoError(t, err)
	require.NotNil(t, def)
	assert.Equal(t, DefinitionService, def.Kind)
}

func TestIndex_References_Type(t *testing.T) {
	ss := snap(t, "/t.thrift", "struct User {}\nstruct Foo { 1: User user, 2: list<User> users, }\nservice Svc { User get(1: i32 id); }")
	_ = parseOne(t, ss, fu("/t.thrift"))

	hits, err := NewIndex(ss).References(ctx, fu("/t.thrift"), "User", cache.RefFieldType, cache.RefSignatureType)
	require.NoError(t, err)
	require.Len(t, hits, 3)
}

func TestIndex_References_ExceptionRule(t *testing.T) {
	ss := snap(t, "/t.thrift", "exception Bad {}\nstruct Foo { 1: Bad bad, }\nservice Svc { void f() throws (1: Bad e); }")
	_ = parseOne(t, ss, fu("/t.thrift"))

	hits, err := NewIndex(ss).References(ctx, fu("/t.thrift"), "Bad", cache.RefSignatureType)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "Bad", hits[0].Text)
}

func TestIndex_References_ConstValue(t *testing.T) {
	ss := snap(t, "/t.thrift", "const i32 MAX = 10\nstruct Foo { 1: i32 id = MAX, }")
	_ = parseOne(t, ss, fu("/t.thrift"))

	hits, err := NewIndex(ss).References(ctx, fu("/t.thrift"), "MAX", cache.RefConstValue)
	require.NoError(t, err)
	require.Len(t, hits, 1)
}

func TestIndex_QualifiedValues(t *testing.T) {
	ss := snap(t, "/t.thrift", "enum Color { RED = 0, BLUE = 1 }\nstruct Foo { 1: i32 id = Color.RED, }\nconst i32 C = Color.BLUE")
	_ = parseOne(t, ss, fu("/t.thrift"))

	hits, err := NewIndex(ss).QualifiedValues(ctx, fu("/t.thrift"), "Color")
	require.NoError(t, err)
	require.Len(t, hits, 2)
	for _, h := range hits {
		assert.Equal(t, "Color", h.Text)
	}
}

func TestIndex_ReferencingFiles(t *testing.T) {
	ss := crossSnap(t, "/a.thrift", `include "b.thrift"`, "/b.thrift", "")
	_ = parseOne(t, ss, fu("/a.thrift"))
	files := NewIndex(ss).ReferencingFiles(fu("/b.thrift"))
	require.Len(t, files, 1)
	assert.Equal(t, fu("/a.thrift"), files[0])
}

func TestIndex_FindInWorkspace(t *testing.T) {
	ss := crossSnap(t, "/a.thrift", "struct User {}", "/b.thrift", "struct Account {}")
	_ = parseOne(t, ss, fu("/a.thrift"))
	_ = parseOne(t, ss, fu("/b.thrift"))

	def, err := NewIndex(ss).FindInWorkspace(ctx, "Account")
	require.NoError(t, err)
	require.NotNil(t, def)
	assert.Equal(t, fu("/b.thrift"), def.File)
	assert.Equal(t, DefinitionStruct, def.Kind)
}

func TestRefKindsFor(t *testing.T) {
	assert.Equal(t, []cache.RefKind{cache.RefSignatureType}, refKindsFor(DefinitionException))
	assert.Equal(t, []cache.RefKind{cache.RefFieldType, cache.RefSignatureType}, refKindsFor(DefinitionStruct))
	assert.Equal(t, []cache.RefKind{cache.RefServiceExtends}, refKindsFor(DefinitionService))
	assert.Equal(t, []cache.RefKind{cache.RefConstValue}, refKindsFor(DefinitionConst))
}

// --- helpers ---

func snap(t *testing.T, file, content string) *cache.Snapshot {
	t.Helper()
	return cache.BuildSnapshotForTest([]*cache.FileChange{{
		URI: fu(file), Version: 0, Content: []byte(content), From: cache.FileChangeTypeDidOpen,
	}})
}

// crossSnap builds a snapshot with two files, parsed in dependency order
// (includes first), so the include graph resolves correctly.
func crossSnap(t *testing.T, fa, ca, fb, cb string) *cache.Snapshot {
	t.Helper()
	ss := cache.BuildSnapshotForTest([]*cache.FileChange{
		{URI: fu(fb), Version: 0, Content: []byte(cb), From: cache.FileChangeTypeDidOpen},
		{URI: fu(fa), Version: 0, Content: []byte(ca), From: cache.FileChangeTypeDidOpen},
	})
	return ss
}

func fu(p string) uri.URI { u, _ := uri.Parse("file://" + p); return u }

func parseOne(t *testing.T, ss *cache.Snapshot, u uri.URI) *cache.ParsedFile {
	t.Helper()
	pf, err := ss.Parse(context.Background(), u)
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
