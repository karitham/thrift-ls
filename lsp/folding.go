package lsp

import (
	"context"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/source"
)

func (s *Server) foldingRanges(ctx context.Context, params *protocol.FoldingRangeParams) ([]protocol.FoldingRange, error) {
	return withSnapshot(ctx, s.session, params.TextDocument.URI, func(ss *cache.Snapshot) ([]protocol.FoldingRange, error) {
		return source.Ranges(ctx, ss, params.TextDocument.URI), nil
	})
}
