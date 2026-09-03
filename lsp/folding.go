package lsp

import (
	"context"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/source"
	"github.com/karitham/thrift-ls/store"
)

func (s *Server) foldingRanges(ctx context.Context, params *protocol.FoldingRangeParams) ([]protocol.FoldingRange, error) {
	return withView(s.viewOf, params.TextDocument.URI, func(view *store.View) ([]protocol.FoldingRange, error) {
		return source.Ranges(ctx, view, params.TextDocument.URI), nil
	})
}
