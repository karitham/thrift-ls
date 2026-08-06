package symbols

import (
	"context"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

// DocumentSymbols returns the document symbols of a file, in source order.
func DocumentSymbols(ctx context.Context, ss *cache.Snapshot, file uri.URI) []*protocol.DocumentSymbol {
	res := make([]*protocol.DocumentSymbol, 0)
	pf, err := ss.Parse(ctx, file)
	if err != nil {
		return res
	}
	if pf.AST() == nil {
		return res
	}

	doc := pf.AST()
	for _, node := range doc.Nodes {
		child := nodeSymbol(doc, node)
		if child != nil {
			res = append(res, child)
		}
	}
	return res
}

//go:fix inline

// nameRange returns the LSP range of an identifier.
func nameRange(doc *syntax.Document, id *syntax.Identifier) protocol.Range {
	if id == nil {
		return protocol.Range{}
	}
	start, end := doc.Range(id)
	return protocol.Range{
		Start: protocol.Position{
			Line:      uint32(start.Line - 1),
			Character: uint32(start.Col - 1),
		},
		End: protocol.Position{
			Line:      uint32(end.Line - 1),
			Character: uint32(end.Col - 1),
		},
	}
}

// nodeSymbol builds the symbol for a top-level definition.
func nodeSymbol(doc *syntax.Document, node syntax.Node) *protocol.DocumentSymbol {
	switch v := node.(type) {
	case *syntax.Typedef:
		return typedefSymbol(doc, v)
	case *syntax.Const:
		return constSymbol(doc, v)
	case *syntax.Struct:
		switch v.Kind {
		case syntax.StructDecl:
			return structSymbol(doc, v, "Struct", protocol.SymbolKindStruct)
		case syntax.UnionDecl:
			return structSymbol(doc, v, "Union", protocol.SymbolKindStruct)
		case syntax.ExceptionDecl:
			return structSymbol(doc, v, "Exception", protocol.SymbolKindStruct)
		}
	case *syntax.Enum:
		return enumSymbol(doc, v)
	case *syntax.Service:
		return serviceSymbol(doc, v)
	}
	return nil
}

func structSymbol(doc *syntax.Document, st *syntax.Struct, detail string, kind protocol.SymbolKind) *protocol.DocumentSymbol {
	res := &protocol.DocumentSymbol{
		Name:           st.Name.Text,
		Detail:         &detail,
		Kind:           kind,
		Range:          nameRange(doc, st.Name),
		SelectionRange: nameRange(doc, st.Name),
	}
	for _, field := range st.Fields {
		child := fieldSymbol(doc, field)
		if child != nil {
			res.Children = append(res.Children, *child)
		}
	}
	return res
}

func enumSymbol(doc *syntax.Document, enum *syntax.Enum) *protocol.DocumentSymbol {
	res := &protocol.DocumentSymbol{
		Name:           enum.Name.Text,
		Detail:         new("Enum"),
		Kind:           protocol.SymbolKindEnum,
		Range:          nameRange(doc, enum.Name),
		SelectionRange: nameRange(doc, enum.Name),
	}
	for _, value := range enum.Values {
		child := &protocol.DocumentSymbol{
			Name:           value.Name.Text,
			Kind:           protocol.SymbolKindEnumMember,
			Range:          nameRange(doc, value.Name),
			SelectionRange: nameRange(doc, value.Name),
		}
		res.Children = append(res.Children, *child)
	}
	return res
}

func serviceSymbol(doc *syntax.Document, svc *syntax.Service) *protocol.DocumentSymbol {
	res := &protocol.DocumentSymbol{
		Name:           svc.Name.Text,
		Kind:           protocol.SymbolKindInterface,
		Range:          nameRange(doc, svc.Name),
		SelectionRange: nameRange(doc, svc.Name),
	}
	for _, fn := range svc.Functions {
		child := &protocol.DocumentSymbol{
			Name:           fn.Name.Text,
			Kind:           protocol.SymbolKindFunction,
			Range:          nameRange(doc, fn.Name),
			SelectionRange: nameRange(doc, fn.Name),
		}
		res.Children = append(res.Children, *child)
	}
	return res
}

func fieldSymbol(doc *syntax.Document, field *syntax.Field) *protocol.DocumentSymbol {
	return &protocol.DocumentSymbol{
		Name:           field.Name.Text,
		Kind:           protocol.SymbolKindField,
		Range:          nameRange(doc, field.Name),
		SelectionRange: nameRange(doc, field.Name),
	}
}

func typedefSymbol(doc *syntax.Document, td *syntax.Typedef) *protocol.DocumentSymbol {
	return &protocol.DocumentSymbol{
		Name:           td.Name.Text,
		Detail:         new("Typedef"),
		Kind:           protocol.SymbolKindTypeParameter,
		Range:          nameRange(doc, td.Name),
		SelectionRange: nameRange(doc, td.Name),
	}
}

func constSymbol(doc *syntax.Document, cst *syntax.Const) *protocol.DocumentSymbol {
	return &protocol.DocumentSymbol{
		Name:           cst.Name.Text,
		Detail:         new("Const"),
		Kind:           protocol.SymbolKindConstant,
		Range:          nameRange(doc, cst.Name),
		SelectionRange: nameRange(doc, cst.Name),
	}
}
