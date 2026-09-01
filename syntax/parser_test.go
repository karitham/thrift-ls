package syntax

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// isNilNode reports whether a Node is nil or a typed nil pointer.
func isNilNode(n Node) bool {
	if n == nil {
		return true
	}

	v := reflect.ValueOf(n)

	return v.Kind() == reflect.Pointer && v.IsNil()
}

func parseOK(t *testing.T, src string) *Document {
	t.Helper()

	doc, errs := Parse([]byte(src))
	for _, err := range errs {
		if err.Severity == SeverityError {
			t.Fatalf("unexpected parse errors: %v", errs)
		}
	}

	checkDoc(t, doc)

	return doc
}

// checkDoc validates structural invariants of a parsed document: every node's
// token range is well-formed and its scalar texts match the token stream.
func checkDoc(t *testing.T, doc *Document) {
	t.Helper()

	var walk func(n Node)

	walk = func(n Node) {
		if isNilNode(n) {
			return
		}

		start, end := n.TokStart(), n.TokEnd()
		if start < 0 || end >= len(doc.Tokens) || start > end {
			t.Errorf("node %T has invalid token range [%d, %d] of %d tokens", n, start, end, len(doc.Tokens))

			return
		}

		tokText := func(i int) string { return doc.Tokens[i].Text }

		switch v := n.(type) {
		case *Identifier:
			if got := tokText(v.TokStart()); got != v.Text {
				t.Errorf("identifier %q does not match token %q", v.Text, got)
			}
		case *ConstValue:
			if v.Text != "" && v.Text != tokText(v.TokStart()) {
				t.Errorf("const value %q does not match token %q", v.Text, tokText(v.TokStart()))
			}

			for _, item := range v.List {
				walk(item)
			}

			for _, entry := range v.Map {
				walk(entry.Key)
				walk(entry.Value)
			}
		case *Field:
			for _, sa := range v.Structured {
				walk(sa)
			}

			walk(v.Type)
			walk(v.Name)
			walk(v.Value)
			walk(v.Annotations)
		case *Const:
			for _, sa := range v.Structured {
				walk(sa)
			}

			walk(v.Type)
			walk(v.Name)
			walk(v.Value)
		case *Typedef:
			for _, sa := range v.Structured {
				walk(sa)
			}

			walk(v.Type)
			walk(v.Name)
			walk(v.Annotations)
		case *Enum:
			for _, sa := range v.Structured {
				walk(sa)
			}

			walk(v.Name)
			walk(v.Annotations)

			for _, val := range v.Values {
				walk(val)
			}
		case *EnumValue:
			walk(v.Name)
			walk(v.Annotations)
		case *Struct:
			for _, sa := range v.Structured {
				walk(sa)
			}

			walk(v.Name)
			walk(v.Annotations)

			for _, f := range v.Fields {
				walk(f)
			}
		case *Service:
			for _, sa := range v.Structured {
				walk(sa)
			}

			walk(v.Name)
			walk(v.Extends)
			walk(v.Annotations)

			for _, f := range v.Functions {
				walk(f)
			}
		case *Function:
			for _, sa := range v.Structured {
				walk(sa)
			}

			walk(v.Type)
			walk(v.Name)
			walk(v.Annotations)

			for _, a := range v.Args {
				walk(a)
			}

			if v.Throws != nil {
				for _, f := range v.Throws.Fields {
					walk(f)
				}
			}
		case *Namespace:
			for _, sa := range v.Structured {
				walk(sa)
			}

			walk(v.Name)
			walk(v.Annotations)
		case *FieldType:
			walk(v.Ident)
			walk(v.KeyType)
			walk(v.ValueType)
			walk(v.Annotations)
		case *Annotations:
			for _, a := range v.Items {
				walk(a)
			}
		case *Annotation:
			walk(v.Name)
		case *StructuredAnnotation:
			walk(v.Name)
		}
	}
	for _, n := range doc.Nodes {
		walk(n)
	}
}

// top returns the first top-level node of the given type.
func top[T Node](t *testing.T, doc *Document) T {
	t.Helper()

	for _, n := range doc.Nodes {
		if v, ok := n.(T); ok {
			return v
		}
	}

	var zero T
	t.Fatalf("no node of type %T in document", zero)

	return zero
}

func TestParseStructs(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		check func(t *testing.T, doc *Document)
	}{
		{
			name: "fields with ids and requiredness",
			src: `struct User {
  1: required i64 id
  2: optional string name
  3: list<i32> scores
}`,
			check: func(t *testing.T, doc *Document) {
				s := top[*Struct](t, doc)
				if s.Kind != StructDecl {
					t.Errorf("kind = %v, want struct", s.Kind)
				}

				if got := s.Name.Text; got != "User" {
					t.Errorf("name = %q", got)
				}

				if len(s.Fields) != 3 {
					t.Fatalf("fields = %d, want 3", len(s.Fields))
				}

				f := s.Fields[0]
				if f.FieldID == nil || f.FieldID.Text != "1" {
					t.Errorf("field 0 id = %v", f.FieldID)
				}

				if f.Req == nil || f.Req.Kind != TokenRequired {
					t.Errorf("field 0 req = %v", f.Req)
				}

				if f.Type.Kind != TypeBase || f.Type.Base != TokenI64 {
					t.Errorf("field 0 type = %+v", f.Type)
				}

				if f.Name.Text != "id" {
					t.Errorf("field 0 name = %q", f.Name.Text)
				}

				if s.Fields[2].Type.Kind != TypeList || s.Fields[2].Type.ValueType.Base != TokenI32 {
					t.Errorf("field 2 type = %+v", s.Fields[2].Type)
				}
			},
		},
		{
			name: "implicit ids and defaults",
			src: `struct S {
  i32 a
  string b = "x"
  i64 c = 42
}`,
			check: func(t *testing.T, doc *Document) {
				s := top[*Struct](t, doc)
				if s.Fields[0].FieldID != nil {
					t.Errorf("implicit id should be nil, got %v", s.Fields[0].FieldID)
				}

				if s.Fields[1].Value.Kind != ValueString || s.Fields[1].Value.Text != `"x"` {
					t.Errorf("field 1 value = %+v", s.Fields[1].Value)
				}

				if s.Fields[2].Value.Kind != ValueInt || s.Fields[2].Value.Text != "42" {
					t.Errorf("field 2 value = %+v", s.Fields[2].Value)
				}
			},
		},
		{
			name: "field reference",
			src: `struct S {
  1: i32 &parent
}`,
			check: func(t *testing.T, doc *Document) {
				s := top[*Struct](t, doc)
				if !s.Fields[0].Reference {
					t.Error("field should be a reference")
				}
			},
		},
		{
			name: "keyword field names",
			src: `struct S {
  string namespace
  i32 cpp_include
}`,
			check: func(t *testing.T, doc *Document) {
				s := top[*Struct](t, doc)
				if s.Fields[0].Name.Text != "namespace" {
					t.Errorf("field 0 name = %q", s.Fields[0].Name.Text)
				}

				if s.Fields[1].Name.Text != "cpp_include" {
					t.Errorf("field 1 name = %q", s.Fields[1].Name.Text)
				}
			},
		},
		{
			name: "type keywords as field names",
			src: `struct S {
  1: string uuid
  2: uuid id
  3: i64 map
  4: i32 string = 1
}`,
			check: func(t *testing.T, doc *Document) {
				s := top[*Struct](t, doc)

				want := []string{"uuid", "id", "map", "string"}
				for i, name := range want {
					if s.Fields[i].Name.Text != name {
						t.Errorf("field %d name = %q, want %q", i, s.Fields[i].Name.Text, name)
					}
				}
				// uuid is both a base type (field 2's type) and a field name
				// (field 1's name); both must survive.
				if s.Fields[1].Type.Base != TokenUUID {
					t.Errorf("field 2 type = %v, want TokenUUID", s.Fields[1].Type.Base)
				}
			},
		},
		{
			name: "empty struct",
			src:  "struct Empty {}",
			check: func(t *testing.T, doc *Document) {
				s := top[*Struct](t, doc)
				if len(s.Fields) != 0 {
					t.Errorf("fields = %d, want 0", len(s.Fields))
				}
			},
		},
		{
			name: "semicolon separators",
			src: `struct S {
  1: i32 a;
  2: string b;
}`,
			check: func(t *testing.T, doc *Document) {
				s := top[*Struct](t, doc)
				if s.Fields[0].Sep != TokenSemicolon || s.Fields[1].Sep != TokenSemicolon {
					t.Errorf("seps = %v, %v", s.Fields[0].Sep, s.Fields[1].Sep)
				}
			},
		},
		{
			name: "annotations on struct and fields",
			src: `struct S {
  1: i32 a (field_anno = "y")
} (struct_anno = "x")`,
			check: func(t *testing.T, doc *Document) {
				s := top[*Struct](t, doc)
				if s.Annotations == nil || len(s.Annotations.Items) != 1 {
					t.Fatalf("struct annotations = %+v", s.Annotations)
				}

				if s.Annotations.Items[0].Name.Text != "struct_anno" || s.Annotations.Items[0].Value.Text != `"x"` {
					t.Errorf("struct annotation = %+v", s.Annotations.Items[0])
				}

				if s.Fields[0].Annotations == nil {
					t.Fatal("field annotations missing")
				}
			},
		},
		{
			name: "type annotations come before the field name",
			src: `struct S {
  1: map<i32, string> (tag = "t") m
}`,
			check: func(t *testing.T, doc *Document) {
				s := top[*Struct](t, doc)

				ft := s.Fields[0].Type
				if ft.Annotations == nil || ft.Annotations.Items[0].Name.Text != "tag" {
					t.Errorf("type annotations = %+v", ft.Annotations)
				}

				if s.Fields[0].Annotations != nil {
					t.Errorf("field annotations should be nil, got %+v", s.Fields[0].Annotations)
				}
			},
		},
		{
			name: "field annotations come after the name",
			src: `struct S {
  1: map<i32, string> m (tag = "t")
}`,
			check: func(t *testing.T, doc *Document) {
				s := top[*Struct](t, doc)
				if s.Fields[0].Type.Annotations != nil {
					t.Errorf("type annotations should be nil, got %+v", s.Fields[0].Type.Annotations)
				}

				if s.Fields[0].Annotations == nil || s.Fields[0].Annotations.Items[0].Name.Text != "tag" {
					t.Errorf("field annotations = %+v", s.Fields[0].Annotations)
				}
			},
		},
		{
			name: "cpp_type on containers",
			src: `struct S {
  1: map cpp_type "std::map" < i32, string > m
  2: list cpp_type "std::vector" < i32 > l
}`,
			check: func(t *testing.T, doc *Document) {
				s := top[*Struct](t, doc)
				if s.Fields[0].Type.CPPType == nil || s.Fields[0].Type.CPPType.Text != `"std::map"` {
					t.Errorf("map cpp_type = %v", s.Fields[0].Type.CPPType)
				}

				if s.Fields[1].Type.CPPType == nil || s.Fields[1].Type.CPPType.Text != `"std::vector"` {
					t.Errorf("list cpp_type = %v", s.Fields[1].Type.CPPType)
				}
			},
		},
		{
			name: "union and exception",
			src: `union U {
  1: i32 a
}

exception E {
  1: string msg
}`,
			check: func(t *testing.T, doc *Document) {
				u := top[*Struct](t, doc)
				if u.Kind != UnionDecl {
					t.Errorf("kind = %v, want union", u.Kind)
				}

				var e *Struct

				for _, n := range doc.Nodes {
					if s, ok := n.(*Struct); ok && s.Kind == ExceptionDecl {
						e = s
					}
				}

				if e == nil || e.Name.Text != "E" {
					t.Errorf("exception missing: %+v", e)
				}
			},
		},
		{
			name: "hex field id",
			src: `struct S {
  0x10: i32 a
}`,
			check: func(t *testing.T, doc *Document) {
				s := top[*Struct](t, doc)
				if s.Fields[0].FieldID == nil || s.Fields[0].FieldID.Text != "0x10" {
					t.Errorf("field id = %v", s.Fields[0].FieldID)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := parseOK(t, tt.src)
			tt.check(t, doc)
		})
	}
}

func TestParseConsts(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		check func(t *testing.T, doc *Document)
	}{
		{
			name: "scalar values",
			src: `const i16 a = 1
const i32 b = 0xa1
const i64 c = -42
const double d = 1.33333333
const double e = 1.3333e11
const double f = 1E11
const string g = "hello"
const string h = 'single'
const bool i = true
const bool j = false`,
			check: func(t *testing.T, doc *Document) {
				var kinds []ConstValueKind
				for _, n := range doc.Nodes {
					kinds = append(kinds, n.(*Const).Value.Kind)
				}

				want := []ConstValueKind{ValueInt, ValueInt, ValueInt, ValueDouble, ValueDouble, ValueDouble, ValueString, ValueString, ValueInt, ValueInt}
				if len(kinds) != len(want) {
					t.Fatalf("consts = %d, want %d", len(kinds), len(want))
				}

				for i := range want {
					if kinds[i] != want[i] {
						t.Errorf("const %d kind = %v, want %v", i, kinds[i], want[i])
					}
				}
				// Raw text preserved.
				if doc.Nodes[1].(*Const).Value.Text != "0xa1" {
					t.Errorf("hex text = %q", doc.Nodes[1].(*Const).Value.Text)
				}

				if doc.Nodes[4].(*Const).Value.Text != "1.3333e11" {
					t.Errorf("double text = %q", doc.Nodes[4].(*Const).Value.Text)
				}
			},
		},
		{
			name: "identifier and negative values",
			src: `const i32 a = OTHER_CONST
const i32 b = -1
const double c = -1.5`,
			check: func(t *testing.T, doc *Document) {
				if doc.Nodes[0].(*Const).Value.Kind != ValueIdent || doc.Nodes[0].(*Const).Value.Text != "OTHER_CONST" {
					t.Errorf("ident value = %+v", doc.Nodes[0].(*Const).Value)
				}

				if doc.Nodes[1].(*Const).Value.Text != "-1" {
					t.Errorf("negative int = %q", doc.Nodes[1].(*Const).Value.Text)
				}

				if doc.Nodes[2].(*Const).Value.Text != "-1.5" {
					t.Errorf("negative double = %q", doc.Nodes[2].(*Const).Value.Text)
				}
			},
		},
		{
			name: "dotted identifier value",
			src:  "const string s = foo.bar.baz",
			check: func(t *testing.T, doc *Document) {
				if v := doc.Nodes[0].(*Const).Value; v.Text != "foo.bar.baz" {
					t.Errorf("dotted ident = %q", v.Text)
				}
			},
		},
		{
			name: "list values",
			src: `const list<i32> a = [1, 2, 3]
const list<string> b = []
const list<list<i32>> c = [[1], [2, 3]]`,
			check: func(t *testing.T, doc *Document) {
				a := doc.Nodes[0].(*Const).Value
				if a.Kind != ValueList || len(a.List) != 3 {
					t.Fatalf("list a = %+v", a)
				}

				if a.List[1].Text != "2" {
					t.Errorf("list item = %q", a.List[1].Text)
				}

				if b := doc.Nodes[1].(*Const).Value; len(b.List) != 0 {
					t.Errorf("empty list = %+v", b)
				}

				c := doc.Nodes[2].(*Const).Value
				if len(c.List) != 2 || len(c.List[1].List) != 2 {
					t.Errorf("nested list = %+v", c)
				}
			},
		},
		{
			name: "map values",
			src: `const map<string, i32> a = {"x": 1, "y": 2}
const map<i32, string> b = {}`,
			check: func(t *testing.T, doc *Document) {
				a := doc.Nodes[0].(*Const).Value
				if a.Kind != ValueMap || len(a.Map) != 2 {
					t.Fatalf("map a = %+v", a)
				}

				if a.Map[0].Key.Text != `"x"` || a.Map[0].Value.Text != "1" {
					t.Errorf("map entry = %+v", a.Map[0])
				}

				if b := doc.Nodes[1].(*Const).Value; len(b.Map) != 0 {
					t.Errorf("empty map = %+v", b)
				}
			},
		},
		{
			name: "nested map in list",
			src:  `const list<map<string, i32>> a = [{"k": 1}]`,
			check: func(t *testing.T, doc *Document) {
				a := doc.Nodes[0].(*Const).Value
				if len(a.List) != 1 || len(a.List[0].Map) != 1 {
					t.Errorf("nested = %+v", a)
				}
			},
		},
		{
			name: "trailing separators",
			src:  "const list<i32> a = [1, 2,];",
			check: func(t *testing.T, doc *Document) {
				c := doc.Nodes[0].(*Const)
				if len(c.Value.List) != 2 {
					t.Errorf("list = %+v", c.Value)
				}

				if c.Sep != TokenSemicolon {
					t.Errorf("const sep = %v", c.Sep)
				}
			},
		},
		{
			name: "const type is container",
			src:  "const map<string, list<i32>> m = {}",
			check: func(t *testing.T, doc *Document) {
				c := doc.Nodes[0].(*Const)
				if c.Type.Kind != TypeMap || c.Type.ValueType.Kind != TypeList {
					t.Errorf("type = %+v", c.Type)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := parseOK(t, tt.src)
			tt.check(t, doc)
		})
	}
}

func TestParseEnums(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		check func(t *testing.T, doc *Document)
	}{
		{
			name: "values with and without ints",
			src: `enum E {
  A = 1,
  B,
  C = 0x10,
  D
}`,
			check: func(t *testing.T, doc *Document) {
				e := top[*Enum](t, doc)
				if len(e.Values) != 4 {
					t.Fatalf("values = %d", len(e.Values))
				}

				if e.Values[0].Value == nil || e.Values[0].Value.Text != "1" {
					t.Errorf("A value = %v", e.Values[0].Value)
				}

				if e.Values[1].Value != nil {
					t.Errorf("B should auto-increment, got %v", e.Values[1].Value)
				}

				if e.Values[2].Value.Text != "0x10" {
					t.Errorf("C value = %v", e.Values[2].Value)
				}

				if e.Values[3].Sep != 0 {
					t.Errorf("D sep = %v", e.Values[3].Sep)
				}
			},
		},
		{
			name: "negative enum value",
			src:  "enum E {\n  A = -1\n}",
			check: func(t *testing.T, doc *Document) {
				e := top[*Enum](t, doc)
				if e.Values[0].Value.Text != "-1" {
					t.Errorf("value = %v", e.Values[0].Value)
				}
			},
		},
		{
			name: "annotations on enum and values",
			src: `enum E {
  A (a_anno = "y")
  B
} (e_anno = "x")`,
			check: func(t *testing.T, doc *Document) {
				e := top[*Enum](t, doc)
				if e.Annotations == nil || e.Values[0].Annotations == nil {
					t.Fatalf("annotations missing: %+v %+v", e.Annotations, e.Values[0].Annotations)
				}
			},
		},
		{
			name: "semicolon separators",
			src:  "enum E {\n  A = 1;\n  B;\n}",
			check: func(t *testing.T, doc *Document) {
				e := top[*Enum](t, doc)
				if e.Values[0].Sep != TokenSemicolon || e.Values[1].Sep != TokenSemicolon {
					t.Errorf("seps = %v, %v", e.Values[0].Sep, e.Values[1].Sep)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := parseOK(t, tt.src)
			tt.check(t, doc)
		})
	}
}

func TestParseServices(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		check func(t *testing.T, doc *Document)
	}{
		{
			name: "functions with args and throws",
			src: `service UserService {
  User getUser(1: i64 id) throws (NotFound e)
  oneway void ping()
  void update(1: User user, 2: string note)
}`,
			check: func(t *testing.T, doc *Document) {
				s := top[*Service](t, doc)
				if len(s.Functions) != 3 {
					t.Fatalf("functions = %d", len(s.Functions))
				}

				f := s.Functions[0]
				if f.Type.Kind != TypeIdent || f.Type.Ident.Text != "User" {
					t.Errorf("getUser type = %+v", f.Type)
				}

				if f.Throws == nil || len(f.Throws.Fields) != 1 {
					t.Fatalf("throws = %+v", f.Throws)
				}

				if f.Throws.Fields[0].Type.Ident.Text != "NotFound" {
					t.Errorf("throws type = %+v", f.Throws.Fields[0].Type)
				}

				ping := s.Functions[1]
				if ping.Oneway == nil || ping.Oneway.Kind != TokenOneway || ping.Void == nil {
					t.Errorf("ping = %+v", ping)
				}

				if len(s.Functions[2].Args) != 2 {
					t.Errorf("update args = %d", len(s.Functions[2].Args))
				}
			},
		},
		{
			name: "extends",
			src:  "service Child extends Parent {\n  void f()\n}",
			check: func(t *testing.T, doc *Document) {
				s := top[*Service](t, doc)
				if s.Extends == nil || s.Extends.Text != "Parent" {
					t.Errorf("extends = %+v", s.Extends)
				}
			},
		},
		{
			name: "async is deprecated oneway",
			src:  "service S {\n  async void f()\n}",
			check: func(t *testing.T, doc *Document) {
				s := top[*Service](t, doc)
				if s.Functions[0].Oneway == nil || s.Functions[0].Oneway.Kind != TokenAsync {
					t.Errorf("oneway = %v", s.Functions[0].Oneway)
				}
			},
		},
		{
			name: "function annotations",
			src: `service S {
  void f() (f_anno = "x")
  void g() throws (E e) (g_anno = "y")
}`,
			check: func(t *testing.T, doc *Document) {
				s := top[*Service](t, doc)
				if s.Functions[0].Annotations == nil {
					t.Error("f annotations missing")
				}

				if s.Functions[1].Annotations == nil || s.Functions[1].Annotations.Items[0].Name.Text != "g_anno" {
					t.Errorf("g annotations = %+v", s.Functions[1].Annotations)
				}
			},
		},
		{
			name: "empty service",
			src:  "service S {}",
			check: func(t *testing.T, doc *Document) {
				if s := top[*Service](t, doc); len(s.Functions) != 0 {
					t.Errorf("functions = %d", len(s.Functions))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := parseOK(t, tt.src)
			tt.check(t, doc)
		})
	}
}

func TestParseHeaders(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		check func(t *testing.T, doc *Document)
	}{
		{
			name: "includes and namespaces",
			src: `include "shared.thrift"
cpp_include "base.h"

namespace java com.example
namespace * global`,
			check: func(t *testing.T, doc *Document) {
				if len(doc.Nodes) != 4 {
					t.Fatalf("nodes = %d: %+v", len(doc.Nodes), doc.Nodes)
				}

				inc := doc.Nodes[0].(*Include)
				if inc.Path.Text != `"shared.thrift"` {
					t.Errorf("include path = %q", inc.Path.Text)
				}

				cpp := doc.Nodes[1].(*CPPInclude)
				if cpp.Path.Text != `"base.h"` {
					t.Errorf("cpp include = %q", cpp.Path.Text)
				}

				ns := doc.Nodes[2].(*Namespace)
				if ns.Scope.Text != "java" || ns.Name.Text != "com.example" {
					t.Errorf("namespace = %+v", ns)
				}

				if doc.Nodes[3].(*Namespace).Scope.Kind != TokenStar {
					t.Errorf("star scope = %+v", doc.Nodes[3].(*Namespace).Scope)
				}
			},
		},
		{
			name: "namespace with annotations",
			src:  `namespace go thrift (go_tag = "x")`,
			check: func(t *testing.T, doc *Document) {
				ns := doc.Nodes[0].(*Namespace)
				if ns.Annotations == nil || ns.Annotations.Items[0].Name.Text != "go_tag" {
					t.Errorf("annotations = %+v", ns.Annotations)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := parseOK(t, tt.src)
			tt.check(t, doc)
		})
	}
}

func TestParseTypedefs(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		check func(t *testing.T, doc *Document)
	}{
		{
			name: "simple and container typedefs",
			src: `typedef i64 Timestamp
typedef map<string, list<i32>> Index`,
			check: func(t *testing.T, doc *Document) {
				td := doc.Nodes[0].(*Typedef)
				if td.Type.Base != TokenI64 || td.Name.Text != "Timestamp" {
					t.Errorf("typedef = %+v", td)
				}

				if doc.Nodes[1].(*Typedef).Type.Kind != TypeMap {
					t.Errorf("container typedef = %+v", doc.Nodes[1].(*Typedef))
				}
			},
		},
		{
			name: "annotations and separators",
			src:  `typedef string Id (id_type = "uuid");`,
			check: func(t *testing.T, doc *Document) {
				td := doc.Nodes[0].(*Typedef)
				if td.Annotations == nil || td.Sep != TokenSemicolon {
					t.Errorf("typedef = %+v", td)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := parseOK(t, tt.src)
			tt.check(t, doc)
		})
	}
}

func TestParseAnnotations(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		check func(t *testing.T, doc *Document)
	}{
		{
			name: "bare annotation defaults to implicit value",
			src:  "typedef i32 T (foo)",
			check: func(t *testing.T, doc *Document) {
				td := doc.Nodes[0].(*Typedef)

				a := td.Annotations.Items[0]
				if a.Name.Text != "foo" || a.Value != nil {
					t.Errorf("bare annotation = %+v", a)
				}
			},
		},
		{
			name: "string values",
			src:  `typedef i32 T (a = "1", b = "x", c = 'y')`,
			check: func(t *testing.T, doc *Document) {
				td := doc.Nodes[0].(*Typedef)

				items := td.Annotations.Items
				if len(items) != 3 {
					t.Fatalf("items = %d", len(items))
				}

				if items[0].Value.Text != `"1"` || items[1].Value.Text != `"x"` || items[2].Value.Text != `'y'` {
					t.Errorf("values = %v, %v, %v", items[0].Value, items[1].Value, items[2].Value)
				}

				if items[0].Sep != TokenComma || items[1].Sep != TokenComma {
					t.Errorf("seps = %v, %v", items[0].Sep, items[1].Sep)
				}
			},
		},
		{
			name: "semicolon and missing separators",
			src:  "typedef i32 T (a = \"1\"; b = \"2\" c = \"3\")",
			check: func(t *testing.T, doc *Document) {
				td := doc.Nodes[0].(*Typedef)

				items := td.Annotations.Items
				if len(items) != 3 {
					t.Fatalf("items = %d", len(items))
				}

				if items[0].Sep != TokenSemicolon || items[1].Sep != 0 || items[2].Sep != 0 {
					t.Errorf("seps = %v, %v, %v", items[0].Sep, items[1].Sep, items[2].Sep)
				}
			},
		},
		{
			name: "empty annotation group",
			src:  "typedef i32 T ()",
			check: func(t *testing.T, doc *Document) {
				td := doc.Nodes[0].(*Typedef)
				if td.Annotations == nil || len(td.Annotations.Items) != 0 {
					t.Errorf("annotations = %+v", td.Annotations)
				}
			},
		},
		{
			name: "multiline annotations",
			src: `typedef i32 T (
  a = "1",
  b = "2"
)`,
			check: func(t *testing.T, doc *Document) {
				td := doc.Nodes[0].(*Typedef)
				if len(td.Annotations.Items) != 2 {
					t.Fatalf("items = %d", len(td.Annotations.Items))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := parseOK(t, tt.src)
			tt.check(t, doc)
		})
	}
}

// TestParseStructuredAnnotations covers @Name <value> annotations, the
// upfluence compiler's structured annotation syntax.
func TestParseStructuredAnnotations(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		check func(t *testing.T, doc *Document)
	}{
		{
			name: "map value before a definition",
			src:  "@naming.PreviouslyKnownAs{'namespace_': 'x'}\nservice Foo {}",
			check: func(t *testing.T, doc *Document) {
				s := top[*Service](t, doc)

				if len(s.Structured) != 1 {
					t.Fatalf("structured = %d, want 1", len(s.Structured))
				}

				sa := s.Structured[0]
				if sa.Name.Text != "naming.PreviouslyKnownAs" {
					t.Errorf("name = %q", sa.Name.Text)
				}

				if sa.Value == nil || sa.Value.Kind != ValueMap || len(sa.Value.Map) != 1 {
					t.Fatalf("value = %+v", sa.Value)
				}

				if sa.Value.Map[0].Key.Text != `'namespace_'` || sa.Value.Map[0].Value.Text != `'x'` {
					t.Errorf("entry = %+v", sa.Value.Map[0])
				}

				if s.TokStart() != sa.TokStart() {
					t.Errorf("service span starts at %d, want annotation start %d", s.TokStart(), sa.TokStart())
				}
			},
		},
		{
			name: "parenthesized scalar values",
			src:  "@a.B(1)\n@c.D(-2.5)\n@e.F('x')\ntypedef i32 T",
			check: func(t *testing.T, doc *Document) {
				td := top[*Typedef](t, doc)

				if len(td.Structured) != 3 {
					t.Fatalf("structured = %d, want 3", len(td.Structured))
				}

				kinds := []ConstValueKind{td.Structured[0].Value.Kind, td.Structured[1].Value.Kind, td.Structured[2].Value.Kind}
				if kinds[0] != ValueInt || kinds[1] != ValueDouble || kinds[2] != ValueString {
					t.Errorf("value kinds = %v", kinds)
				}

				if td.Structured[0].Value.Text != "1" || td.Structured[2].Value.Text != `'x'` {
					t.Errorf("scalar texts = %q, %q", td.Structured[0].Value.Text, td.Structured[2].Value.Text)
				}
			},
		},
		{
			name: "identifier and list values",
			src:  "@a.B(SomeEnum.VALUE)\n@c.D ['x', 'y']\nstruct S {}",
			check: func(t *testing.T, doc *Document) {
				s := top[*Struct](t, doc)

				if len(s.Structured) != 2 {
					t.Fatalf("structured = %d, want 2", len(s.Structured))
				}

				if s.Structured[0].Value.Kind != ValueIdent || s.Structured[0].Value.Text != "SomeEnum.VALUE" {
					t.Errorf("ident value = %+v", s.Structured[0].Value)
				}

				if s.Structured[1].Value.Kind != ValueList || len(s.Structured[1].Value.List) != 2 {
					t.Errorf("list value = %+v", s.Structured[1].Value)
				}
			},
		},
		{
			name: "annotations on a field and a function",
			src:  "struct S {\n  @dep.Deprecated(1) 1: i32 a\n}\nservice Svc {\n  @rpc.Auth('admin') string whoami()\n}",
			check: func(t *testing.T, doc *Document) {
				s := top[*Struct](t, doc)

				if len(s.Fields[0].Structured) != 1 || s.Fields[0].Structured[0].Name.Text != "dep.Deprecated" {
					t.Fatalf("field structured = %+v", s.Fields[0].Structured)
				}

				if s.Fields[0].FieldID == nil || s.Fields[0].FieldID.Text != "1" {
					t.Errorf("field id after annotation = %v", s.Fields[0].FieldID)
				}

				svc := top[*Service](t, doc)

				if len(svc.Functions) != 1 {
					t.Fatalf("functions = %d", len(svc.Functions))
				}

				f := svc.Functions[0]
				if len(f.Structured) != 1 || f.Structured[0].Name.Text != "rpc.Auth" {
					t.Fatalf("function structured = %+v", f.Structured)
				}

				if f.TokStart() != f.Structured[0].TokStart() {
					t.Errorf("function span starts at %d, want annotation start %d", f.TokStart(), f.Structured[0].TokStart())
				}
			},
		},
		{
			name: "annotations on a field in a function argument",
			src:  "service S {\n  void f(@meta.Range(10) i32 a)\n}",
			check: func(t *testing.T, doc *Document) {
				svc := top[*Service](t, doc)
				arg := svc.Functions[0].Args[0]

				if len(arg.Structured) != 1 || arg.Structured[0].Name.Text != "meta.Range" {
					t.Fatalf("arg structured = %+v", arg.Structured)
				}
			},
		},
		{
			name: "multiple annotations before one definition",
			src:  "@a.B(1)\n@c.D ['x']\nenum E { A }",
			check: func(t *testing.T, doc *Document) {
				e := top[*Enum](t, doc)

				if len(e.Structured) != 2 {
					t.Fatalf("structured = %d, want 2", len(e.Structured))
				}
			},
		},
		{
			name: "mixed with legacy annotations",
			src:  "@a.B(1)\nstruct S { 1: i32 x } (legacy = \"y\")",
			check: func(t *testing.T, doc *Document) {
				s := top[*Struct](t, doc)

				if len(s.Structured) != 1 || s.Annotations == nil || s.Annotations.Items[0].Name.Text != "legacy" {
					t.Errorf("structured = %v, legacy = %v", s.Structured, s.Annotations)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := parseOK(t, tt.src)
			tt.check(t, doc)
		})
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantErrs []string // substrings, one per expected error
	}{
		{
			name:     "unterminated struct",
			src:      "struct S {",
			wantErrs: []string{"expected '}' to close struct"},
		},
		{
			name:     "missing brace",
			src:      "struct S",
			wantErrs: []string{"expected '{' after struct name"},
		},
		{
			name:     "missing field name",
			src:      "struct S {\n  1: i32\n}",
			wantErrs: []string{"expected field name"},
		},
		{
			name:     "missing struct name",
			src:      "struct {}",
			wantErrs: []string{"expected struct name"},
		},
		{
			name:     "annotation value must be string",
			src:      "typedef i32 T (x = 1, y = \"2\")",
			wantErrs: []string{"expected string literal annotation value"},
		},
		{
			name:     "bare structured annotation has no value",
			src:      "@Deprecated\nstruct S {}",
			wantErrs: []string{`expected annotation value after "Deprecated"`},
		},
		{
			name:     "structured annotation with no name",
			src:      "@\nstruct S {}",
			wantErrs: []string{"expected annotation name"},
		},
		{
			name:     "structured annotation value must be a constant",
			src:      "@a.B struct S {}",
			wantErrs: []string{`expected annotation value after "a.B"`},
		},
		{
			name:     "structured annotation before nothing",
			src:      "struct A {}\n@x.Y(1)",
			wantErrs: []string{"expected definition after annotation"},
		},
		{
			name:     "parenthesized annotation value must be scalar: map",
			src:      "@a.B({'k': 'v'})\nstruct S {}",
			wantErrs: []string{"parenthesized annotation value must be a scalar constant"},
		},
		{
			name:     "parenthesized annotation value must be scalar: list",
			src:      "@a.B(['x'])\nstruct S {}",
			wantErrs: []string{"parenthesized annotation value must be a scalar constant"},
		},
		{
			name:     "unquoted include",
			src:      "include foo.thrift\nstruct S {}",
			wantErrs: []string{"expected include path string"},
		},
		{
			name:     "garbage at top level recovers",
			src:      ", , ,\nstruct S {}",
			wantErrs: []string{"unexpected token"},
		},
		{
			name:     "bad field recovers to closing brace",
			src:      "struct S {\n  1: void bad\n}",
			wantErrs: []string{"expected type"},
		},
		{
			name:     "missing const value",
			src:      "const i32 x =",
			wantErrs: []string{"expected constant value"},
		},
		{
			name:     "unterminated list constant",
			src:      "const list<i32> x = [1, 2",
			wantErrs: []string{"expected ']' to close list constant"},
		},
		{
			name:     "bad function recovers",
			src:      "service S {\n  void ()\n  void ok()\n}",
			wantErrs: []string{"expected function name"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, errs := Parse([]byte(tt.src))
			if doc == nil {
				t.Fatal("parse returned nil document")
			}

			var got []string

			for _, err := range errs {
				if err.Severity == SeverityError {
					got = append(got, err.Error())
				}
			}

			if len(got) != len(tt.wantErrs) {
				t.Fatalf("got %d errors (%v), want %d", len(got), got, len(tt.wantErrs))
			}

			for i, want := range tt.wantErrs {
				if !strings.Contains(got[i], want) {
					t.Errorf("error %d = %q, want substring %q", i, got[i], want)
				}
			}
		})
	}
}

func TestParseWarnings(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantMsg []string
	}{
		{
			name:    "async deprecated",
			src:     "service S {\n  async void f()\n}",
			wantMsg: []string{"async is deprecated"},
		},
		{
			name:    "byte deprecated",
			src:     "struct S {\n  1: byte b\n}",
			wantMsg: []string{"byte is deprecated"},
		},
		{
			name:    "slist unsupported",
			src:     "struct S {\n  1: slist s\n}",
			wantMsg: []string{"slist is no longer supported"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs := Parse([]byte(tt.src))

			var warns []string

			for _, err := range errs {
				if err.Severity == SeverityWarning {
					warns = append(warns, err.Error())
				}
			}

			if len(warns) != len(tt.wantMsg) {
				t.Fatalf("warnings = %v, want %d", warns, len(tt.wantMsg))
			}

			for i, want := range tt.wantMsg {
				if !strings.Contains(warns[i], want) {
					t.Errorf("warning %d = %q, want substring %q", i, warns[i], want)
				}
			}
		})
	}
}

func TestParseRoundTripCorpus(t *testing.T) {
	// A corpus of valid files that must parse with zero errors; the invariant
	// checker in parseOK validates node ranges and texts for each.
	corpus := []string{
		"",
		"include \"a.thrift\"\ninclude \"b.thrift\"\n\nnamespace java com.example\n\nstruct A {\n  1: i32 id\n}",
		"cpp_include \"<vector>\"\n\nstruct S {\n  1: list cpp_type \"std::vector\" < i32 > v\n}",
		"enum Color {\n  RED = 1,\n  GREEN = 2\n}\n\nstruct S {\n  1: Color c\n}",
		"service S extends Base {\n  oneway void fire()\n  i32 calc(1: i32 a, 2: i32 b) throws (E e, F f)\n}\n",
		"const map<string, list<i32>> M = {\"a\": [1, 2], \"b\": []}",
		"typedef map<i32, map<string, bool>> Nested\n",
		"struct S {\n  // leading comment\n  1: i32 a // trailing comment\n}",
		"/** doc comment */\nstruct S {\n  1: i32 a\n}",
		"namespace * all\nstruct S {}",
		"const double d = .5\nconst double e = 1.5e-3\nconst double f = -0.25E+2",
		"struct S {\n  1: required i32 a = 0x10 (anno = \"x\", bare)\n}",
	}
	for i, src := range corpus {
		t.Run("case-"+strconv.Itoa(i), func(t *testing.T) {
			parseOK(t, src)
		})
	}
}
