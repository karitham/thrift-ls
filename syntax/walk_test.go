package syntax

import (
	"reflect"
	"slices"
	"testing"
)

// TestWalkPreorder pins the traversal order over every child-bearing node
// kind: Document, Namespace, Include, Const, Enum, Typedef, Struct, Service,
// nested container types, const list/map values, and function signatures.
// The order must match nodeChildren's child enumeration exactly.
func TestWalkPreorder(t *testing.T) {
	src := `namespace go demo

include "shared.thrift"

const i32 MAX = 3

enum Color {
  RED,
  BLUE = 2
}

typedef shared.Color Alias

struct User {
  1: required shared.Color fav;
  2: optional map<string, list<i32>> idx = {"a": [1]};
}

service Svc extends Base {
  User get(1: i32 id) throws (string err)
}
`
	doc, errs := Parse([]byte(src))
	for _, e := range errs {
		if e.Severity == SeverityError {
			t.Fatalf("unexpected parse errors: %v", errs)
		}
	}

	want := []string{
		"*syntax.Document",
		"*syntax.Namespace",
		"*syntax.Identifier", // demo
		"*syntax.Include",
		"*syntax.Const",
		"*syntax.FieldType",  // i32
		"*syntax.Identifier", // MAX
		"*syntax.ConstValue", // 3
		"*syntax.Enum",
		"*syntax.Identifier", // Color
		"*syntax.EnumValue",  // RED
		"*syntax.Identifier", // RED
		"*syntax.EnumValue",  // BLUE
		"*syntax.Identifier", // BLUE
		"*syntax.Typedef",
		"*syntax.FieldType",  // shared.Color
		"*syntax.Identifier", // shared.Color
		"*syntax.Identifier", // Alias
		"*syntax.Struct",
		"*syntax.Identifier", // User
		"*syntax.Field",      // fav
		"*syntax.FieldType",  // shared.Color
		"*syntax.Identifier", // shared.Color
		"*syntax.Identifier", // fav
		"*syntax.Field",      // idx
		"*syntax.FieldType",  // map<...>
		"*syntax.FieldType",  // string key
		"*syntax.FieldType",  // list<...>
		"*syntax.FieldType",  // i32 element
		"*syntax.Identifier", // idx
		"*syntax.ConstValue", // {"a": [1]}
		"*syntax.ConstValue", // "a"
		"*syntax.ConstValue", // [1]
		"*syntax.ConstValue", // 1
		"*syntax.Service",
		"*syntax.Identifier", // Svc
		"*syntax.Identifier", // Base
		"*syntax.Function",
		"*syntax.Identifier", // get
		"*syntax.FieldType",  // User
		"*syntax.Identifier", // User
		"*syntax.Field",      // id
		"*syntax.FieldType",  // i32
		"*syntax.Identifier", // id
		"*syntax.Throws",
		"*syntax.Field",      // err
		"*syntax.FieldType",  // string
		"*syntax.Identifier", // err
	}

	got := []string{}
	Walk(doc, func(n Node) bool {
		got = append(got, reflect.TypeOf(n).String())

		return true
	})
	if !slices.Equal(got, want) {
		t.Errorf("preorder mismatch:\n got (%d):\n%v\nwant (%d):\n%v", len(got), got, len(want), want)
	}
}

// TestWalkCollectsIdentifiers checks that a document's identifier names are
// all reachable through Walk: definition names, field names, references,
// namespace names, and extends targets.
func TestWalkCollectsIdentifiers(t *testing.T) {
	src := `namespace go demo

include "shared.thrift"

typedef shared.Color Alias

struct User {
  1: shared.Color fav;
}

service Svc extends Base {
  void ping()
}
`
	doc, errs := Parse([]byte(src))
	for _, e := range errs {
		if e.Severity == SeverityError {
			t.Fatalf("unexpected parse errors: %v", errs)
		}
	}

	var got []string

	Walk(doc, func(n Node) bool {
		if id, ok := n.(*Identifier); ok {
			got = append(got, id.Text)
		}

		return true
	})

	want := []string{"demo", "shared.Color", "shared.Color", "Alias", "User", "fav", "Svc", "Base", "ping"}
	slices.Sort(got)
	slices.Sort(want)

	if !slices.Equal(got, want) {
		t.Errorf("identifiers = %v, want %v", got, want)
	}
}

// TestWalkDescendControl verifies that returning false skips a subtree.
func TestWalkDescendControl(t *testing.T) {
	doc, errs := Parse([]byte(`struct A { 1: i32 x }`))
	for _, e := range errs {
		if e.Severity == SeverityError {
			t.Fatalf("unexpected parse errors: %v", errs)
		}
	}

	var visits int

	Walk(doc, func(Node) bool {
		visits++

		return false
	})

	if visits != 1 {
		t.Errorf("visits = %d, want 1 (only the root)", visits)
	}
}

// TestNodeChildrenLeaves pins the explicit leaf node types: they have no
// children, so traversals stop at them by design rather than by accident.
func TestNodeChildrenLeaves(t *testing.T) {
	leaves := []Node{
		&Identifier{},
		&Include{},
		&CPPInclude{},
		&Annotation{},
		&Annotations{},
	}

	for _, leaf := range leaves {
		if got := nodeChildren(leaf); got != nil {
			t.Errorf("%T children = %v, want nil", leaf, got)
		}
	}

	if got := nodeChildren(&StructuredAnnotation{Name: &Identifier{}}); len(got) != 1 {
		t.Errorf("StructuredAnnotation children = %v, want [Name]", got)
	}
}

// TestEachAnnotation pins the coverage of EveryAnnotation: every group on
// namespaces, typedefs, structs, fields, enum values, services, functions,
// arguments, throws members, and container types is visited.
func TestEachAnnotation(t *testing.T) {
	src := `namespace go demo (ns.note = "x")

typedef i32 T (td.a)

enum E { RED } (e.a)
enum E2 { GREEN (green.a) }

struct S {
  1: required string f (f.ann);
  2: required map<string, i32> (map.ann) m;
} (s.ann = "1")

service Svc {
  void go(1: string arg (arg.ann)) throws (1: string err (err.ann))
} (svc.ann)
`
	doc, errs := Parse([]byte(src))
	for _, e := range errs {
		if e.Severity == SeverityError {
			t.Fatalf("unexpected parse errors: %v", errs)
		}
	}

	var got []string

	doc.EachAnnotation(func(a *Annotations) {
		for _, item := range a.Items {
			got = append(got, item.Name.Text)
		}
	})

	want := []string{"ns.note", "td.a", "e.a", "green.a", "s.ann", "f.ann", "map.ann", "svc.ann", "arg.ann", "err.ann"}
	slices.Sort(got)
	slices.Sort(want)

	if !slices.Equal(got, want) {
		t.Errorf("annotations = %v, want %v", got, want)
	}
}
