package codejump

import (
	"context"
	"strings"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/lsputils"
	"github.com/karitham/thrift-ls/syntax"
)

// DefinitionKind identifies the kind of a resolved definition.
type DefinitionKind uint8

const (
	DefinitionNone DefinitionKind = iota
	DefinitionStruct
	DefinitionUnion
	DefinitionException
	DefinitionEnum
	DefinitionTypedef
	DefinitionConst
	DefinitionEnumValue
	DefinitionService
)

// definitionFiles returns the files to search for a definition, in order.
// A qualified name ("base.User") resolves to the include file; an
// unqualified name searches the current file first, then each included
// file.
func definitionFiles(ctx context.Context, ss *cache.Snapshot, file uri.URI, ast *syntax.Document, name string) []uri.URI {
	include, _ := lsputils.ParseIdent(file, ast.Includes(), name)
	if include != "" {
		resolver := ss.Resolver()

		path := resolver.GetIncludePath(ast, include)
		if path == "" {
			// Doesn't match any include path; treat as local.
			return []uri.URI{file}
		}

		return []uri.URI{resolver.ResolveInclude(file, path)}
	}

	files := []uri.URI{file}
	resolver := ss.Resolver()

	for _, inc := range ast.Includes() {
		if path := lsputils.IncludePathText(inc); path != "" {
			files = append(files, resolver.ResolveInclude(file, path))
		}
	}

	return files
}

// GetExceptionNode finds an exception declaration by name.
func GetExceptionNode(ast *syntax.Document, name string) *syntax.Struct {
	if ast == nil {
		return nil
	}

	for _, excep := range ast.Exceptions() {
		if excep.Name != nil && excep.Name.Text == name {
			return excep
		}
	}

	return nil
}

// GetStructNode finds a struct declaration by name.
func GetStructNode(ast *syntax.Document, name string) *syntax.Struct {
	if ast == nil {
		return nil
	}

	for _, st := range ast.Structs() {
		if st.Name != nil && st.Name.Text == name {
			return st
		}
	}

	return nil
}

// GetUnionNode finds a union declaration by name.
func GetUnionNode(ast *syntax.Document, name string) *syntax.Struct {
	if ast == nil {
		return nil
	}

	for _, st := range ast.Unions() {
		if st.Name != nil && st.Name.Text == name {
			return st
		}
	}

	return nil
}

// GetEnumNode finds an enum declaration by name.
func GetEnumNode(ast *syntax.Document, name string) *syntax.Enum {
	if ast == nil {
		return nil
	}

	for _, st := range ast.Enums() {
		if st.Name != nil && st.Name.Text == name {
			return st
		}
	}

	return nil
}

// GetEnumNodeByEnumValue finds the enum declaring an enum value reference
// like "EnumName.VALUE" or a bare value name.
func GetEnumNodeByEnumValue(ast *syntax.Document, enumValueName string) *syntax.Enum {
	if ast == nil {
		return nil
	}

	enumName, _, found := strings.Cut(enumValueName, ".")
	if found {
		return GetEnumNode(ast, enumName)
	}

	for _, enum := range ast.Enums() {
		for _, value := range enum.Values {
			if value.Name != nil && value.Name.Text == enumValueName {
				return enum
			}
		}
	}

	return nil
}

// GetEnumValueIdentifierNode returns the identifier of an enum value
// referenced as "EnumName.VALUE", or as a bare name.
func GetEnumValueIdentifierNode(ast *syntax.Document, name string) *syntax.Identifier {
	if ast == nil {
		return nil
	}

	enumName, identifier, found := strings.Cut(name, ".")
	if !found {
		// Bare name: search all enum values.
		for _, enum := range ast.Enums() {
			for _, enumValue := range enum.Values {
				if enumValue.Name != nil && enumValue.Name.Text == name {
					return enumValue.Name
				}
			}
		}

		return nil
	}

	for _, enum := range ast.Enums() {
		if enum.Name == nil || enum.Name.Text != enumName {
			continue
		}

		for _, enumValue := range enum.Values {
			if enumValue.Name != nil && enumValue.Name.Text == identifier {
				return enumValue.Name
			}
		}
	}

	return nil
}

// GetConstNode finds a const declaration by name.
func GetConstNode(ast *syntax.Document, name string) *syntax.Const {
	if ast == nil {
		return nil
	}

	for _, cst := range ast.Consts() {
		if cst.Name != nil && cst.Name.Text == name {
			return cst
		}
	}

	return nil
}

// GetConstIdentifierNode returns the name identifier of a const
// declaration.
func GetConstIdentifierNode(ast *syntax.Document, name string) *syntax.Identifier {
	if ast == nil {
		return nil
	}

	for _, cst := range ast.Consts() {
		if cst.Name != nil && cst.Name.Text == name {
			return cst.Name
		}
	}

	return nil
}

// GetTypedefNode finds a typedef declaration by name.
func GetTypedefNode(ast *syntax.Document, name string) *syntax.Typedef {
	if ast == nil {
		return nil
	}

	for _, td := range ast.Typedefs() {
		if td.Name != nil && td.Name.Text == name {
			return td
		}
	}

	return nil
}

// GetServiceNode finds a service declaration by name.
func GetServiceNode(ast *syntax.Document, name string) *syntax.Service {
	if ast == nil {
		return nil
	}

	for _, svc := range ast.Services() {
		if svc.Name != nil && svc.Name.Text == name {
			return svc
		}
	}

	return nil
}

var basicType = map[string]struct{}{
	"map":    {},
	"set":    {},
	"list":   {},
	"string": {},
	"i16":    {},
	"i32":    {},
	"i64":    {},
	"i8":     {},
	"double": {},
	"bool":   {},
	"byte":   {},
	"binary": {},
	"uuid":   {},
	"slist":  {},
}

var containerType = map[string]struct{}{
	"map":  {},
	"set":  {},
	"list": {},
}

// IsBasicType reports whether t is a built-in base type.
func IsBasicType(t string) bool {
	_, ok := basicType[t]

	return ok
}

// IsContainerType reports whether t is a container keyword.
func IsContainerType(t string) bool {
	_, ok := containerType[t]

	return ok
}

// typeReferenceName returns the referenced type name of a FieldType, or ""
// for base types and containers.
func typeReferenceName(ft *syntax.FieldType) string {
	if ft == nil {
		return ""
	}

	if ft.Kind == syntax.TypeIdent && ft.Ident != nil {
		return ft.Ident.Text
	}

	return ""
}
