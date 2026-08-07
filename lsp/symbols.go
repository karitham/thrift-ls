package lsp

import (
	"context"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/symbols"
)

func (s *Server) documentSymbol(ctx context.Context, params *protocol.DocumentSymbolParams) (result protocol.DocumentSymbolSlice, err error) {
	file := params.TextDocument.URI

	view, err := s.session.ViewOf(file)
	if err != nil {
		return nil, err
	}

	ss, release := view.Snapshot()
	defer release()

	syms := symbols.DocumentSymbols(ctx, ss, file)

	for i := range syms {
		result = append(result, *syms[i])
	}

	return result, err
}
