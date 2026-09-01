package sema

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

// TestIndexResolutionMemo pins that Index resolutions are memoized per
// (file, name, resolver): repeated resolutions of the same reference are
// identical, unresolved names stay unresolved, and the same name resolved
// as a type versus a value does not collide.
func TestIndexResolutionMemo(t *testing.T) {
	view := cache.BuildViewForTest([]*cache.FileChange{
		{
			URI:     "file:///base.thrift",
			Version: 0,
			Content: []byte("struct User { 1: i32 id }\nenum Color { RED = 1 }\nconst i32 MAX = 10\nservice Svc {}\n"),
			From:    cache.FileChangeTypeDidOpen,
		},
		{
			URI:     "file:///app.thrift",
			Version: 0,
			Content: []byte("include \"base.thrift\"\nstruct User { 1: string name }\nstruct S { 1: base.User u, 2: i32 x = base.Color.RED }\n"),
			From:    cache.FileChangeTypeDidOpen,
		},
	})

	ix := NewIndex(view)
	pf := parseOne(t, view, fu("/app.thrift"))

	tests := []struct {
		name     string
		resolve  func() (*Resolved, error)
		wantFile string // URI path of the definition file; "" for unresolved
		wantName string
		wantKind DefinitionKind
	}{
		{
			name:     "qualified type through an include",
			resolve:  func() (*Resolved, error) { return ix.ResolveType(t.Context(), pf, ft("base.User")) },
			wantFile: "/base.thrift",
			wantName: "User",
			wantKind: DefinitionStruct,
		},
		{
			name:     "local definition wins over the include",
			resolve:  func() (*Resolved, error) { return ix.ResolveType(t.Context(), pf, ft("User")) },
			wantFile: "/app.thrift",
			wantName: "User",
			wantKind: DefinitionStruct,
		},
		{
			name: "qualified enum value",
			resolve: func() (*Resolved, error) {
				return ix.ResolveValue(t.Context(), pf, &syntax.ConstValue{Kind: syntax.ValueIdent, Text: "base.Color.RED"})
			},
			wantFile: "/base.thrift",
			wantName: "RED",
			wantKind: DefinitionEnumValue,
		},
		{
			name: "qualified service",
			resolve: func() (*Resolved, error) {
				return ix.ResolveService(t.Context(), pf, &syntax.Identifier{Text: "base.Svc"})
			},
			wantFile: "/base.thrift",
			wantName: "Svc",
			wantKind: DefinitionService,
		},
		{
			name: "const value by bare name",
			resolve: func() (*Resolved, error) {
				return ix.ResolveValue(t.Context(), pf, &syntax.ConstValue{Kind: syntax.ValueIdent, Text: "MAX"})
			},
			wantFile: "/base.thrift",
			wantName: "MAX",
			wantKind: DefinitionConst,
		},
		{
			name:     "unresolved name memoizes nil",
			resolve:  func() (*Resolved, error) { return ix.ResolveType(t.Context(), pf, ft("Nope")) },
			wantFile: "",
			wantKind: DefinitionNone,
		},
		{
			name:     "a name that is a const is not a type",
			resolve:  func() (*Resolved, error) { return ix.ResolveType(t.Context(), pf, ft("MAX")) },
			wantFile: "",
			wantKind: DefinitionNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Resolve twice: the memoized result must be identical.
			first, err := tt.resolve()
			require.NoError(t, err)
			second, err := tt.resolve()
			require.NoError(t, err)
			assert.Equal(t, first, second, "memoized resolution must be stable")

			if tt.wantKind == DefinitionNone {
				assert.Nil(t, first, "expected unresolved")

				return
			}

			require.NotNil(t, first)
			assert.Equal(t, tt.wantFile, first.File.Path())
			assert.Equal(t, tt.wantName, first.Name.Text)
			assert.Equal(t, tt.wantKind, first.Kind)
		})
	}
}

// TestIndexMemoKeyIsolation pins the memo's key: the same bare name
// resolved from different files never collides, and neither do different
// names from the same file.
func TestIndexMemoKeyIsolation(t *testing.T) {
	view := cache.BuildViewForTest([]*cache.FileChange{
		{
			URI:     "file:///a.thrift",
			Version: 0,
			Content: []byte("struct User { 1: i32 id }\n"),
			From:    cache.FileChangeTypeDidOpen,
		},
		{
			URI:     "file:///b.thrift",
			Version: 0,
			Content: []byte("struct User { 1: string name }\n"),
			From:    cache.FileChangeTypeDidOpen,
		},
		{
			URI:     "file:///main.thrift",
			Version: 0,
			Content: []byte("include \"a.thrift\"\ninclude \"b.thrift\"\nstruct S { 1: a.User x, 2: b.User y }\n"),
			From:    cache.FileChangeTypeDidOpen,
		},
	})

	ix := NewIndex(view)
	main := parseOne(t, view, fu("/main.thrift"))
	fromB := parseOne(t, view, fu("/b.thrift"))

	tests := []struct {
		name     string
		from     *cache.ParsedFile
		text     string
		wantFile string
	}{
		// Same bare name, different referencing files: the memo keys on
		// the file, so the two resolutions cannot collide.
		{"bare User from main resolves through the first include", main, "User", "/a.thrift"},
		{"bare User from b resolves locally", fromB, "User", "/b.thrift"},
		// Same file, different names.
		{"a.User from main", main, "a.User", "/a.thrift"},
		{"b.User from main", main, "b.User", "/b.thrift"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def, err := ix.ResolveType(t.Context(), tt.from, ft(tt.text))
			require.NoError(t, err)
			require.NotNil(t, def)
			assert.Equal(t, tt.wantFile, def.File.Path())
		})
	}
}

// TestSemanticAnalysisSkipsBrokenFile verifies that a file with parse
// errors does not fail the semantic analysis run: the Parse checker owns
// parse errors, and the analysis proceeds (or skips) without erroring.
func TestSemanticAnalysisSkipsBrokenFile(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"unterminated struct", "struct S { 1: "},
		{"garbage tokens", "foo bar baz"},
		{"unclosed annotation", "struct S (x = "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := cache.BuildViewForTest([]*cache.FileChange{
				{
					URI:     "file:///f.thrift",
					Version: 0,
					Content: []byte(tt.content),
					From:    cache.FileChangeTypeDidOpen,
				},
			})

			report, err := New(Config{}, []Analyzer{EachFile(&SemanticAnalysis{})}).Run(t.Context(), view, []uri.URI{"file:///f.thrift"})
			require.NoError(t, err, "a broken file must not fail the diagnostics run")
			assert.NotNil(t, report)
		})
	}
}
