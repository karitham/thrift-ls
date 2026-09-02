package lsp

import (
	"context"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/source"
)

func (s *Server) documentHighlight(ctx context.Context, params *protocol.DocumentHighlightParams) ([]protocol.DocumentHighlight, error) {
	return withView(s.viewOf, params.TextDocument.URI, func(view *cache.View) ([]protocol.DocumentHighlight, error) {
		return source.Highlight(ctx, view, params.TextDocument.URI, params.Position)
	})
}
