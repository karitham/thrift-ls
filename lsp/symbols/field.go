package symbols

import (
	"github.com/joyme123/protocol"
	"github.com/joyme123/thrift-ls/format"
	"github.com/joyme123/thrift-ls/lsp/lsputils"
	"github.com/joyme123/thrift-ls/parser"
)

func FieldSymbol(field *parser.Field) *protocol.DocumentSymbol {
	if field.IsBadNode() || field.ChildrenBadNode() {
		return nil
	}

	detail := ""
	if field.RequiredKeyword != nil {
		detail = field.RequiredKeyword.Literal.Text + " "
	}
	// Use default options for symbol display
	opts := format.Options{}
	detail += format.MustFormatFieldType(field.FieldType, opts)

	res := &protocol.DocumentSymbol{
		Name:           field.Identifier.Name.Text,
		Detail:         detail,
		Kind:           protocol.SymbolKindField,
		Range:          lsputils.ASTNodeToRange(field.Identifier.Name),
		SelectionRange: lsputils.ASTNodeToRange(field.Identifier.Name),
	}

	return res
}
