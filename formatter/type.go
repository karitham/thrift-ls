package formatter

import (
	"strings"

	"github.com/karitham/thrift-ls/doc"
	"github.com/karitham/thrift-ls/syntax"
)

// fieldType formats a type reference: base keyword, identifier, or
// container. Containers and base types carry optional annotations.
func (f *formatter) fieldType(t *syntax.FieldType) doc.Doc {
	parts := []doc.Doc{doc.Text(typeText(t))}
	parts = append(parts, f.annotationsDoc(t.Annotations))
	return doc.Concat(parts)
}

// typeText renders a type reference as plain text, matching the compiler's
// spelling: containers use "map<k, v>", optional cpp_type comes after the
// container keyword ("list cpp_type \"x\" <i32>"), matching the grammar.
func typeText(t *syntax.FieldType) string {
	if t == nil {
		return ""
	}
	switch t.Kind {
	case syntax.TypeBase:
		return t.Base.String()
	case syntax.TypeIdent:
		return t.Ident.Text
	case syntax.TypeMap:
		return containerText("map", t.CPPType, typeText(t.KeyType), typeText(t.ValueType))
	case syntax.TypeList:
		return containerText("list", t.CPPType, typeText(t.ValueType))
	case syntax.TypeSet:
		return containerText("set", t.CPPType, typeText(t.ValueType))
	}
	return ""
}

func containerText(kind string, cppType *syntax.Token, inner ...string) string {
	prefix := kind
	if cppType != nil {
		// The grammar reads cpp_type before '<': "list cpp_type \"x\" <i32>".
		return prefix + " cpp_type " + cppType.Text + " <" + strings.Join(inner, ", ") + ">"
	}
	return prefix + "<" + strings.Join(inner, ", ") + ">"
}
