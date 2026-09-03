package lsp

import (
	"context"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/source"
	"github.com/karitham/thrift-ls/store"
)

func (s *Server) documentHighlight(ctx context.Context, params *protocol.DocumentHighlightParams) ([]protocol.DocumentHighlight, error) {
	return withView(s.viewOf, params.TextDocument.URI, func(view *store.View) ([]protocol.DocumentHighlight, error) {
		return source.Highlight(ctx, view, params.TextDocument.URI, params.Position)
	})
}
