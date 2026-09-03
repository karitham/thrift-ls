package source

import (
	"context"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/store"
	"github.com/karitham/thrift-ls/syntax"
)

// DocumentSymbols returns the document symbols of a file, in source order.
func DocumentSymbols(ctx context.Context, view *store.View, file uri.URI) []*protocol.DocumentSymbol {
	res := make([]*protocol.DocumentSymbol, 0)

	pf, err := view.Parse(ctx, file)
	if err != nil {
		return res
	}

	if pf.AST() == nil {
		return res
	}

	doc := pf.AST()
	for _, node := range doc.Nodes {
		child := nodeSymbol(pf, node)
		if child != nil {
			res = append(res, child)
		}
	}

	return res
}

//go:fix inline

// nameRange returns the LSP range of an identifier.
func nameRange(pf *store.ParsedFile, id *syntax.Identifier) protocol.Range {
	if id == nil {
		return protocol.Range{}
	}

	start, end := pf.AST().Range(id)

	return toLSPRange(pf, start, end)
}

// nodeSymbol builds the symbol for a top-level definition.
func nodeSymbol(pf *store.ParsedFile, node syntax.Node) *protocol.DocumentSymbol {
	switch v := node.(type) {
	case *syntax.Typedef:
		return typedefSymbol(pf, v)
	case *syntax.Const:
		return constSymbol(pf, v)
	case *syntax.Struct:
		switch v.Kind {
		case syntax.StructDecl:
			return structSymbol(pf, v, "Struct", protocol.SymbolKindStruct)
		case syntax.UnionDecl:
			return structSymbol(pf, v, "Union", protocol.SymbolKindInterface)
		case syntax.ExceptionDecl:
			return structSymbol(pf, v, "Exception", protocol.SymbolKindClass)
		}
	case *syntax.Enum:
		return enumSymbol(pf, v)
	case *syntax.Service:
		return serviceSymbol(pf, v)
	}

	return nil
}

func structSymbol(pf *store.ParsedFile, st *syntax.Struct, detail string, kind protocol.SymbolKind) *protocol.DocumentSymbol {
	res := &protocol.DocumentSymbol{
		Name:           st.Name.Text,
		Detail:         &detail,
		Kind:           kind,
		Range:          nameRange(pf, st.Name),
		SelectionRange: nameRange(pf, st.Name),
	}
	for _, field := range st.Fields {
		child := fieldSymbol(pf, field)
		if child != nil {
			res.Children = append(res.Children, *child)
		}
	}

	return res
}

func enumSymbol(pf *store.ParsedFile, enum *syntax.Enum) *protocol.DocumentSymbol {
	res := &protocol.DocumentSymbol{
		Name:           enum.Name.Text,
		Detail:         new("Enum"),
		Kind:           protocol.SymbolKindEnum,
		Range:          nameRange(pf, enum.Name),
		SelectionRange: nameRange(pf, enum.Name),
	}
	for _, value := range enum.Values {
		child := &protocol.DocumentSymbol{
			Name:           value.Name.Text,
			Kind:           protocol.SymbolKindEnumMember,
			Range:          nameRange(pf, value.Name),
			SelectionRange: nameRange(pf, value.Name),
		}
		res.Children = append(res.Children, *child)
	}

	return res
}

func serviceSymbol(pf *store.ParsedFile, svc *syntax.Service) *protocol.DocumentSymbol {
	res := &protocol.DocumentSymbol{
		Name:           svc.Name.Text,
		Kind:           protocol.SymbolKindInterface,
		Range:          nameRange(pf, svc.Name),
		SelectionRange: nameRange(pf, svc.Name),
	}
	for _, fn := range svc.Functions {
		child := &protocol.DocumentSymbol{
			Name:           fn.Name.Text,
			Kind:           protocol.SymbolKindFunction,
			Range:          nameRange(pf, fn.Name),
			SelectionRange: nameRange(pf, fn.Name),
		}
		res.Children = append(res.Children, *child)
	}

	return res
}

func fieldSymbol(pf *store.ParsedFile, field *syntax.Field) *protocol.DocumentSymbol {
	return &protocol.DocumentSymbol{
		Name:           field.Name.Text,
		Kind:           protocol.SymbolKindField,
		Range:          nameRange(pf, field.Name),
		SelectionRange: nameRange(pf, field.Name),
	}
}

func typedefSymbol(pf *store.ParsedFile, td *syntax.Typedef) *protocol.DocumentSymbol {
	return &protocol.DocumentSymbol{
		Name:           td.Name.Text,
		Detail:         new("Typedef"),
		Kind:           protocol.SymbolKindTypeParameter,
		Range:          nameRange(pf, td.Name),
		SelectionRange: nameRange(pf, td.Name),
	}
}

func constSymbol(pf *store.ParsedFile, cst *syntax.Const) *protocol.DocumentSymbol {
	return &protocol.DocumentSymbol{
		Name:           cst.Name.Text,
		Detail:         new("Const"),
		Kind:           protocol.SymbolKindConstant,
		Range:          nameRange(pf, cst.Name),
		SelectionRange: nameRange(pf, cst.Name),
	}
}
