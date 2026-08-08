package cache

import (
	"github.com/karitham/thrift-ls/syntax"
)

// RefKind classifies a name reference by the grammar slot it sits in. The
// slot decides what the reference can legally point at: an exception is
// only referenced from signatures, an enum value only from value positions.
type RefKind uint8

const (
	// RefFieldType is a type reference in a field-ish position: struct,
	// union, and exception fields, typedef targets, and const types.
	RefFieldType RefKind = iota + 1
	// RefSignatureType is a type reference in a service signature: a
	// function return type, argument, or throws member.
	RefSignatureType
	// RefConstValue is an identifier in a constant value position: a field
	// default or a const value, possibly qualified ("Color.RED").
	RefConstValue
	// RefServiceExtends is a service extends reference.
	RefServiceExtends
)

// Reference is one name occurrence that resolves to a definition somewhere:
// in this file or in an included one. It is a raw fact — the name text as
// written, uninterpreted. Qualifier parsing ("shared.User" vs
// "shared.thrift.User") and definition matching live in the source layer,
// which knows the include graph.
type Reference struct {
	Kind RefKind

	// Name is the reference text as written: "User", "shared.User", or
	// "shared.thrift.User".
	Name string

	// Node carries the reference's position: *syntax.Identifier for type
	// and service references, *syntax.ConstValue for value references.
	// Ranges come from the owning file's AST and mapper.
	Node syntax.Node
}

// FileIndex is the per-file semantic index: the file's definitions, enum
// values, name references, and annotation names, extracted in a single AST
// walk and cached with the parse. A re-parse replaces the whole
// ParsedFile, so the index never goes stale.
//
// The index answers "what does this file contain". Cross-file questions —
// "where is this name defined", "who references it" — belong to
// source.Index, which composes FileIndexes over the include graph.
type FileIndex struct {
	defs        map[string]syntax.Node
	enumValues  map[string]*syntax.Identifier
	refs        []Reference
	annotations map[string]struct{}
}

// Defs returns the file's top-level definitions indexed by name: structs,
// unions, exceptions, enums, services, consts, and typedefs. The node's
// concrete type identifies the definition kind.
func (x *FileIndex) Defs() map[string]syntax.Node {
	return x.defs
}

// EnumValues returns the file's enum value names indexed by name.
func (x *FileIndex) EnumValues() map[string]*syntax.Identifier {
	return x.enumValues
}

// References returns every name reference in the file, in document order:
// field and signature type references, constant value identifiers, and
// service extends references.
func (x *FileIndex) References() []Reference {
	return x.refs
}

// buildIndex extracts the FileIndex of ast in one walk.
func buildIndex(ast *syntax.Document) *FileIndex {
	x := &FileIndex{
		defs:        make(map[string]syntax.Node),
		enumValues:  make(map[string]*syntax.Identifier),
		annotations: make(map[string]struct{}),
	}
	if ast == nil {
		return x
	}

	collect := &indexWalker{x: x}

	for _, n := range ast.Nodes {
		collect.visit(n)
	}

	return x
}

// indexWalker accumulates the index of one document in a single walk.
type indexWalker struct {
	x *FileIndex
}

// visit walks one top-level node: definitions and enum values bind names,
// references are recorded with the slot classification of their position.
func (w *indexWalker) visit(n syntax.Node) {
	switch v := n.(type) {
	case *syntax.Struct:
		w.x.defs[v.Name.Text] = v
		w.note(v.Annotations)

		for _, f := range v.Fields {
			w.field(f, RefFieldType)
		}
	case *syntax.Enum:
		w.x.defs[v.Name.Text] = v
		w.note(v.Annotations)

		for _, ev := range v.Values {
			w.x.enumValues[ev.Name.Text] = ev.Name
			w.note(ev.Annotations)
		}
	case *syntax.Service:
		w.x.defs[v.Name.Text] = v
		w.note(v.Annotations)

		if v.Extends != nil {
			w.x.refs = append(w.x.refs, Reference{
				Kind: RefServiceExtends,
				Name: v.Extends.Text,
				Node: v.Extends,
			})
		}

		for _, fn := range v.Functions {
			w.note(fn.Annotations)
			w.typ(fn.Type, RefSignatureType)

			for _, a := range fn.Args {
				w.field(a, RefSignatureType)
			}

			if fn.Throws != nil {
				for _, f := range fn.Throws.Fields {
					w.field(f, RefSignatureType)
				}
			}
		}
	case *syntax.Const:
		w.x.defs[v.Name.Text] = v
		w.typ(v.Type, RefFieldType)
		w.value(v.Value)
	case *syntax.Typedef:
		w.x.defs[v.Name.Text] = v
		w.note(v.Annotations)
		w.typ(v.Type, RefFieldType)
	case *syntax.Namespace:
		w.note(v.Annotations)
	}
}

// field records a field's type and default value references, plus its
// annotations.
func (w *indexWalker) field(f *syntax.Field, kind RefKind) {
	w.note(f.Annotations)
	w.typ(f.Type, kind)
	w.value(f.Value)
}

// typ records a type reference: the type name itself and any container
// element types, all in the same slot kind. Annotations on container
// types and the type name itself are also collected.
func (w *indexWalker) typ(ft *syntax.FieldType, kind RefKind) {
	if ft == nil {
		return
	}

	w.note(ft.Annotations)

	if ft.Kind == syntax.TypeIdent && ft.Ident != nil {
		w.x.refs = append(w.x.refs, Reference{Kind: kind, Name: ft.Ident.Text, Node: ft.Ident})
	}

	w.typ(ft.KeyType, kind)
	w.typ(ft.ValueType, kind)
}

// value records an identifier in a value position: a field default, a
// const value, or a nested list/map element, recursively. The boolean
// literals true/false are value identifiers syntactically but not
// references; every other identifier (including qualified forms like
// "Color.RED" or "shared.Color.RED") is.
func (w *indexWalker) value(v *syntax.ConstValue) {
	if v == nil {
		return
	}

	if v.Kind == syntax.ValueIdent && v.Text != "true" && v.Text != "false" {
		w.x.refs = append(w.x.refs, Reference{Kind: RefConstValue, Name: v.Text, Node: v})
	}

	for _, item := range v.List {
		w.value(item)
	}

	for _, entry := range v.Map {
		w.value(entry.Key)
		w.value(entry.Value)
	}
}

// note records every annotation name of the annotations node.
func (w *indexWalker) note(a *syntax.Annotations) {
	if a == nil {
		return
	}

	for _, item := range a.Items {
		w.x.annotations[item.Name.Text] = struct{}{}
	}
}
