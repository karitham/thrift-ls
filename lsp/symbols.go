package lsp

import (
	"context"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/source"
	"github.com/karitham/thrift-ls/store"
)

func (s *Server) documentSymbol(ctx context.Context, params *protocol.DocumentSymbolParams) (result protocol.DocumentSymbolSlice, err error) {
	return withView(s.viewOf, params.TextDocument.URI, func(view *store.View) (protocol.DocumentSymbolSlice, error) {
		syms := source.DocumentSymbols(ctx, view, params.TextDocument.URI)

		result := make(protocol.DocumentSymbolSlice, 0, len(syms))
		for i := range syms {
			result = append(result, *syms[i])
		}

		return result, nil
	})
}
