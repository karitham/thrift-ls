package sema

import (
	"context"
	"strings"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/store"
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
// unqualified name searches the current file first, then every file
// transitively included, so a type visible through a multi-hop include
// chain (A includes B includes C) is found.
func definitionFiles(ctx context.Context, view *store.View, file uri.URI, ast *syntax.Document, name string) []uri.URI {
	include, _ := ParseIdent(file, ast.Includes(), name)
	if include != "" {
		resolver := view.Resolver()

		path := resolver.GetIncludePath(ast, include)
		if path == "" {
			// Doesn't match any include path; treat as local.
			return []uri.URI{file}
		}

		return []uri.URI{resolver.ResolveInclude(ctx, file, path)}
	}

	files := []uri.URI{file}
	seen := map[uri.URI]bool{file: true}
	resolver := view.Resolver()

	var visit func(f uri.URI)

	visit = func(f uri.URI) {
		doc := ast

		if f != file {
			pf, err := view.Parse(ctx, f)
			if err != nil || pf.AST() == nil {
				return
			}

			doc = pf.AST()
		}

		for _, inc := range doc.Includes() {
			if path := inc.PathText(); path != "" {
				incFile := resolver.ResolveInclude(ctx, f, path)
				if seen[incFile] {
					continue
				}

				seen[incFile] = true
				files = append(files, incFile)
				visit(incFile)
			}
		}
	}

	visit(file)

	return files
}

// IsBasicType reports whether t is a built-in base type.
func IsBasicType(t string) bool {
	_, ok := basicType[t]

	return ok
}

// TypeReferenceName returns the referenced type name of a FieldType, or ""
// for base types and containers.
func TypeReferenceName(ft *syntax.FieldType) string {
	if ft == nil {
		return ""
	}

	if ft.Kind == syntax.TypeIdent && ft.Ident != nil {
		return ft.Ident.Text
	}

	return ""
}

// BareName strips the include qualifier from a name: "base.User" becomes
// "User". References in files that include the definition file use the bare
// name, so qualified literals must match against it too.
func BareName(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}

	return name
}

var basicType = map[string]struct{}{
	"bool":   {},
	"byte":   {},
	"i8":     {},
	"i16":    {},
	"i32":    {},
	"i64":    {},
	"double": {},
	"string": {},
	"binary": {},
	"slist":  {},
	"uuid":   {},
}
