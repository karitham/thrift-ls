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
// unqualified name searches the current file first, then every file
// transitively included, so a type visible through a multi-hop include
// chain (A includes B includes C) is found.
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
	seen := map[uri.URI]bool{file: true}
	resolver := ss.Resolver()

	var visit func(f uri.URI)

	visit = func(f uri.URI) {
		doc := ast
		if f != file {
			pf, err := ss.Parse(ctx, f)
			if err != nil || pf.AST() == nil {
				return
			}

			doc = pf.AST()
		}

		for _, inc := range doc.Includes() {
			if path := lsputils.IncludePathText(inc); path != "" {
				incFile := resolver.ResolveInclude(f, path)
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

// bareName strips the include qualifier from a name: "base.User" becomes
// "User". References in files that include the definition file use the bare
// name, so qualified literals must match against it too.
func bareName(name string) string {
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

var containerType = map[string]struct{}{
	"list": {},
	"map":  {},
	"set":  {},
}

// definitionMatches reports whether the node has the expected definition
// kind.
func definitionMatches(n syntax.Node, kind DefinitionKind) bool {
	switch v := n.(type) {
	case *syntax.Struct:
		switch v.Kind {
		case syntax.UnionDecl:
			return kind == DefinitionUnion
		case syntax.ExceptionDecl:
			return kind == DefinitionException
		}

		return kind == DefinitionStruct
	case *syntax.Enum:
		return kind == DefinitionEnum
	case *syntax.Typedef:
		return kind == DefinitionTypedef
	}

	return false
}

// enumOfValue returns the enum declaring the value with the given name, or
// nil.
func enumOfValue(pf *cache.ParsedFile, name string) *syntax.Enum {
	id := pf.EnumValues()[name]
	if id == nil {
		return nil
	}

	// The value identifier's parent enum is reachable through the node
	// path; walk the document to find the enum containing the value.
	for _, n := range pf.AST().Nodes {
		enum, ok := n.(*syntax.Enum)
		if !ok {
			continue
		}

		for _, value := range enum.Values {
			if value.Name == id {
				return enum
			}
		}
	}

	return nil
}
