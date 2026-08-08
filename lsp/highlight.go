package lsp

import (
	"context"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/source"
)

func (s *Server) documentHighlight(ctx context.Context, params *protocol.DocumentHighlightParams) ([]protocol.DocumentHighlight, error) {
	return withSnapshot(s.session, params.TextDocument.URI, func(ss *cache.Snapshot) ([]protocol.DocumentHighlight, error) {
		return source.Highlight(ctx, ss, params.TextDocument.URI, params.Position)
	})
}
