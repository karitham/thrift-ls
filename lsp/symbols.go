package lsp

import (
	"context"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/source"
)

func (s *Server) documentSymbol(ctx context.Context, params *protocol.DocumentSymbolParams) (result protocol.DocumentSymbolSlice, err error) {
	return withSnapshot(ctx, s.session, params.TextDocument.URI, func(ss *cache.Snapshot) (protocol.DocumentSymbolSlice, error) {
		syms := source.DocumentSymbols(ctx, ss, params.TextDocument.URI)

		result := make(protocol.DocumentSymbolSlice, 0, len(syms))
		for i := range syms {
			result = append(result, *syms[i])
		}

		return result, nil
	})
}
