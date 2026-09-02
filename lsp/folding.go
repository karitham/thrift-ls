package lsp

import (
	"context"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/source"
)

func (s *Server) foldingRanges(ctx context.Context, params *protocol.FoldingRangeParams) ([]protocol.FoldingRange, error) {
	return withView(s.viewOf, params.TextDocument.URI, func(view *cache.View) ([]protocol.FoldingRange, error) {
		return source.Ranges(ctx, view, params.TextDocument.URI), nil
	})
}
