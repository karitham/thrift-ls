package syntax

import (
	"strings"
	"testing"
)

func TestDocumentAccessors(t *testing.T) {
	src := `include "a.thrift"
cpp_include "<x>"
namespace go x

const i32 C = 1
typedef i64 T
enum E { A = 1 }
struct S { 1: i32 a }
union U { 1: i32 a }
exception X { 1: string m }
service Svc { void f() }
`
	doc := parseOK(t, src)

	if len(doc.Includes()) != 1 || doc.Includes()[0].Path.Text != `"a.thrift"` {
		t.Errorf("Includes = %+v", doc.Includes())
	}

	if len(doc.CPPIncludes()) != 1 {
		t.Errorf("CPPIncludes = %d", len(doc.CPPIncludes()))
	}

	if len(doc.Namespaces()) != 1 {
		t.Errorf("Namespaces = %d", len(doc.Namespaces()))
	}

	if len(doc.Consts()) != 1 || doc.Consts()[0].Name.Text != "C" {
		t.Errorf("Consts = %+v", doc.Consts())
	}

	if len(doc.Typedefs()) != 1 {
		t.Errorf("Typedefs = %d", len(doc.Typedefs()))
	}

	if len(doc.Enums()) != 1 {
		t.Errorf("Enums = %d", len(doc.Enums()))
	}

	if len(doc.Structs()) != 1 || doc.Structs()[0].Name.Text != "S" {
		t.Errorf("Structs = %+v", doc.Structs())
	}

	if len(doc.Unions()) != 1 {
		t.Errorf("Unions = %d", len(doc.Unions()))
	}

	if len(doc.Exceptions()) != 1 || doc.Exceptions()[0].Name.Text != "X" {
		t.Errorf("Exceptions = %+v", doc.Exceptions())
	}

	if len(doc.Services()) != 1 {
		t.Errorf("Services = %d", len(doc.Services()))
	}
	// Structs must not include unions or exceptions.
	if len(doc.Structs())+len(doc.Unions())+len(doc.Exceptions()) != 3 {
		t.Errorf("struct-like split wrong")
	}
}

func TestDocumentRanges(t *testing.T) {
	src := "struct S {\n  1: i32 a\n}"
	doc := parseOK(t, src)
	s := doc.Structs()[0]

	start, end := doc.Range(s.Name)
	if start.Line != 1 || start.Col != 8 || end.Line != 1 || end.Col != 9 {
		t.Errorf("name range = %+v..%+v", start, end)
	}

	start, end = doc.Range(s)
	if start.Line != 1 || start.Col != 1 || end.Line != 3 || end.Col != 2 {
		t.Errorf("struct range = %+v..%+v", start, end)
	}

	if !doc.Contains(s, start) {
		t.Error("range start should be contained")
	}

	if !doc.Contains(s, end) {
		t.Error("range end should be contained")
	}

	if !doc.Contains(s, Position{Line: 2, Col: 7, Offset: 18}) {
		t.Error("position inside struct should be contained")
	}

	if doc.Contains(s, Position{Line: 5, Col: 1, Offset: 25}) {
		t.Error("position outside struct should not be contained")
	}
}

func TestSearchNodePathByPosition(t *testing.T) {
	src := `struct User {
  1: required i64 id
  2: optional string name = "bob"
}

service Svc extends Base {
  User getUser(1: i64 id) throws (NotFound e)
}

const i32 X = SOME_VALUE
`
	doc := parseOK(t, src)

	// Helper: position of the first occurrence of text in the source.
	posOf := func(text string) Position {
		idx := strings.Index(src, text)
		if idx < 0 {
			t.Fatalf("text %q not found in source", text)
		}

		return doc.TokenPosition(tokIndexAt(doc, idx))
	}

	tests := []struct {
		name string
		text string
		want string // expected deepest node type
	}{
		{"struct name", "User", "*syntax.Identifier"},
		{"field type", "i64", "*syntax.FieldType"},
		{"field name", "id", "*syntax.Identifier"},
		{"type reference", "NotFound", "*syntax.Identifier"},
		{"extends", "Base", "*syntax.Identifier"},
		{"const value", "SOME_VALUE", "*syntax.ConstValue"},
		{"service name", "Svc", "*syntax.Identifier"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := doc.SearchNodePathByPosition(posOf(tt.text))
			if len(path) == 0 {
				t.Fatal("empty path")
			}

			deepest := path[len(path)-1]
			if got := typeName(deepest); got != tt.want {
				t.Errorf("deepest node = %s, want %s (path: %v)", got, tt.want, pathNames(path))
			}
		})
	}

	// Field name identifier: the parent is the field.
	path := doc.SearchNodePathByPosition(posOf("id"))
	if len(path) < 3 {
		t.Fatalf("path too short: %v", pathNames(path))
	}

	if _, ok := path[len(path)-2].(*Field); !ok {
		t.Errorf("parent of field name should be Field: %v", pathNames(path))
	}

	// Struct name identifier: the parent is the struct.
	path = doc.SearchNodePathByPosition(posOf("User"))
	if len(path) < 3 {
		t.Fatalf("path too short: %v", pathNames(path))
	}

	if _, ok := path[len(path)-2].(*Struct); !ok {
		t.Errorf("parent of struct name should be Struct: %v", pathNames(path))
	}

	// Position on nothing (blank line between definitions).
	idx := strings.Index(src, "const i32")
	if idx < 0 {
		t.Fatal("const not found")
	}

	pos := doc.TokenPosition(tokIndexAt(doc, idx))
	pos.Offset -= 1 // the last newline of the blank line before const

	path = doc.SearchNodePathByPosition(pos)
	if len(path) != 1 { // only the document itself
		t.Errorf("expected document-only path, got %v", pathNames(path))
	}
}

func tokIndexAt(doc *Document, offset int) int {
	// Find the last token starting at or before offset.
	idx := 0

	for i, tok := range doc.Tokens {
		if tok.Offset <= offset {
			idx = i
		}
	}

	return idx
}

func typeName(n Node) string {
	switch n.(type) {
	case *Document:
		return "*syntax.Document"
	case *Const:
		return "*syntax.Const"
	case *Typedef:
		return "*syntax.Typedef"
	case *Enum:
		return "*syntax.Enum"
	case *EnumValue:
		return "*syntax.EnumValue"
	case *Struct:
		return "*syntax.Struct"
	case *Service:
		return "*syntax.Service"
	case *Function:
		return "*syntax.Function"
	case *Throws:
		return "*syntax.Throws"
	case *Field:
		return "*syntax.Field"
	case *FieldType:
		return "*syntax.FieldType"
	case *ConstValue:
		return "*syntax.ConstValue"
	case *Identifier:
		return "*syntax.Identifier"
	case *Namespace:
		return "*syntax.Namespace"
	case *Include:
		return "*syntax.Include"
	case *CPPInclude:
		return "*syntax.CPPInclude"
	}

	return "unknown"
}

func pathNames(path []Node) []string {
	names := make([]string, 0, len(path))
	for _, n := range path {
		names = append(names, typeName(n))
	}

	return names
}
