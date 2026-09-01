package source

import (
	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/sema"
	"github.com/karitham/thrift-ls/syntax"
)

// definitionMatches reports whether the node has the expected definition
// kind.
func definitionMatches(n syntax.Node, kind sema.DefinitionKind) bool {
	switch v := n.(type) {
	case *syntax.Struct:
		switch v.Kind {
		case syntax.UnionDecl:
			return kind == sema.DefinitionUnion
		case syntax.ExceptionDecl:
			return kind == sema.DefinitionException
		}

		return kind == sema.DefinitionStruct
	case *syntax.Enum:
		return kind == sema.DefinitionEnum
	case *syntax.Typedef:
		return kind == sema.DefinitionTypedef
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
