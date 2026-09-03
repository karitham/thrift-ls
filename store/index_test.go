package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karitham/thrift-ls/syntax"
)

func TestFileIndex_Defs(t *testing.T) {
	idx := buildIndex(parse(t, `
		struct Foo {}
		union Bar {}
		exception Err {}
		enum Color { RED }
		service Svc {}
		const i32 C = 0
		typedef i32 Age
	`))

	defs := idx.Defs()
	assert.NotNil(t, defs["Foo"])
	assert.NotNil(t, defs["Bar"])
	assert.NotNil(t, defs["Err"])
	assert.NotNil(t, defs["Color"])
	assert.NotNil(t, defs["Svc"])
	assert.NotNil(t, defs["C"])
	assert.NotNil(t, defs["Age"])
}

func TestFileIndex_EnumValues(t *testing.T) {
	idx := buildIndex(parse(t, `
		enum Color { RED = 0, GREEN = 2, BLUE = 3 }
	`))

	all := idx.EnumValues()
	assert.NotNil(t, all["RED"])
	assert.NotNil(t, all["GREEN"])
	assert.NotNil(t, all["BLUE"])
	assert.Equal(t, "RED", all["RED"].Text)
}

func TestFileIndex_References(t *testing.T) {
	idx := buildIndex(parse(t, `
		include "shared.thrift"
		struct Foo {
			1: i32 id,
			2: shared.Bar bar,
			3: list<Baz> items,
		}
		const i32 C = shared.Max

		typedef map<i32, Qux> QuxMap

		service Svc {
			RpcResult do(1: Arg arg) throws (1: Err err);
		}
	`))

	refs := idx.References()

	// field type references: shared.Bar (qualified), Baz, list<Baz> has Baz element
	has := func(name string, kind RefKind) bool {
		for _, r := range refs {
			if r.Name == name && r.Kind == kind {
				return true
			}
		}

		return false
	}

	require.True(t, has("shared.Bar", RefFieldType), "field type")
	require.True(t, has("Baz", RefFieldType), "container element type")
	require.True(t, has("shared.Max", RefConstValue), "const value reference")
	require.True(t, has("Qux", RefFieldType), "typedef target")

	require.True(t, has("RpcResult", RefSignatureType), "return type")
	require.True(t, has("Arg", RefSignatureType), "argument type")
	require.True(t, has("Err", RefSignatureType), "throws type")
}

// TestFileIndex_StructuredAnnotationReferences pins the structured
// annotation names as type references: every @Name occurrence across
// definitions, fields, and functions lands in References() with the
// RefAnnotationType slot.
func TestFileIndex_StructuredAnnotationReferences(t *testing.T) {
	idx := buildIndex(parse(t, `
		@Naming{'ns': 'x'}
		@Tags ['a']
		struct Foo {
			@Dep(1) 1: i32 id,
		}

		@Naming{'ns': 'y'}
		service Svc {
			@Dep(2) void do()
		}
	`))

	refs := idx.References()

	count := func(name string, kind RefKind) int {
		n := 0
		for _, r := range refs {
			if r.Name == name && r.Kind == kind {
				n++
			}
		}

		return n
	}

	require.Equal(t, 2, count("Naming", RefAnnotationType), "definition-level annotations")
	require.Equal(t, 2, count("Dep", RefAnnotationType), "field and function annotations")
	require.Equal(t, 1, count("Tags", RefAnnotationType), "list-valued annotation")
}

func TestFileIndex_ServiceExtends(t *testing.T) {
	idx := buildIndex(parse(t, `
		service Base {}
		service Derived extends Base {}
	`))

	has := false
	for _, r := range idx.References() {
		if r.Kind == RefServiceExtends && r.Name == "Base" {
			has = true
		}
	}

	require.True(t, has)
}

func TestFileIndex_ExcludesTrueFalse(t *testing.T) {
	idx := buildIndex(parse(t, `
		const bool C = true
		const bool D = false
		const i32 E = SOME_CONST
	`))

	names := make([]string, 0, len(idx.References()))
	for _, r := range idx.References() {
		names = append(names, r.Name)
	}
	assert.Contains(t, names, "SOME_CONST", "positive control: real idents are indexed")
	assert.NotContains(t, names, "true")
	assert.NotContains(t, names, "false")
}

func TestFileIndex_Annotations(t *testing.T) {
	// Each sub-test uses an isolated document so parser interactions
	// (struct-level annotations before annotated fields, etc.) do not
	// mask results.
	t.Run("definition", func(t *testing.T) {
		idx := buildIndex(parse(t, `struct Foo (ann = "x") {}`))
		require.Contains(t, idx.annotations, "ann")
	})
	t.Run("field type", func(t *testing.T) {
		idx := buildIndex(parse(t, `struct Foo {
			1: i32 (bar = "y") id,
		}`))
		require.Contains(t, idx.annotations, "bar")
	})
	t.Run("enum", func(t *testing.T) {
		idx := buildIndex(parse(t, `enum Color (colorAnn = "z") { RED }`))
		require.Contains(t, idx.annotations, "colorAnn")
	})
	t.Run("service", func(t *testing.T) {
		idx := buildIndex(parse(t, `service Svc (svcAnn) {
			void f(1: i32 x);
		}`))
		require.Contains(t, idx.annotations, "svcAnn")
	})
	t.Run("namespace", func(t *testing.T) {
		idx := buildIndex(parse(t, `namespace java com.example (langAnn)`))
		require.Contains(t, idx.annotations, "langAnn")
	})
	t.Run("typedef", func(t *testing.T) {
		idx := buildIndex(parse(t, `typedef i32 (typeAnn) Age`))
		require.Contains(t, idx.annotations, "typeAnn")
	})
}

func TestFileIndex_SingleWalkConsistency(t *testing.T) {
	content := `
		struct Foo {
			1: Bar bar,
		}
		enum Color { RED = 0 }
		service Svc extends Base {
			void f(1: i32 x) throws (1: Err e);
		}
	`
	p := mustParse(content)

	// buildIndex output must match an independent syntax.Walk oracle on
	// the same document: every definition and enum value the walker
	// finds, keyed by name.
	wantDefs := map[string]struct{}{}
	wantEnums := map[string]struct{}{}
	syntax.Walk(p, func(n syntax.Node) bool {
		switch v := n.(type) {
		case *syntax.Struct:
			wantDefs[v.Name.Text] = struct{}{}
		case *syntax.Enum:
			wantDefs[v.Name.Text] = struct{}{}
			for _, ev := range v.Values {
				wantEnums[ev.Name.Text] = struct{}{}
			}
		case *syntax.Service:
			wantDefs[v.Name.Text] = struct{}{}
		case *syntax.Const, *syntax.Typedef:
			switch d := n.(type) {
			case *syntax.Const:
				wantDefs[d.Name.Text] = struct{}{}
			case *syntax.Typedef:
				wantDefs[d.Name.Text] = struct{}{}
			}
		}

		return true
	})

	pf := &ParsedFile{ast: p}
	for name := range wantDefs {
		assert.Contains(t, pf.Definitions(), name)
	}
	for name := range wantEnums {
		assert.Contains(t, pf.EnumValues(), name)
	}
	assert.Len(t, pf.Definitions(), len(wantDefs), "index must not add phantom definitions")
}

func mustParse(src string) *syntax.Document {
	ast, _ := syntax.Parse([]byte(src))
	if ast == nil {
		panic("mustParse: fixture produced nil document")
	}

	return ast
}

func parse(t *testing.T, src string) *syntax.Document {
	t.Helper()
	ast, _ := syntax.Parse([]byte(src))
	require.NotNil(t, ast, "fixture must produce a document")

	return ast
}
