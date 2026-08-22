package lsp

import (
	"context"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/source"
)

func (s *Server) documentLink(ctx context.Context, params *protocol.DocumentLinkParams) ([]protocol.DocumentLink, error) {
	return withView(s.session, params.TextDocument.URI, func(view *cache.View) ([]protocol.DocumentLink, error) {
		return source.Links(ctx, view, params.TextDocument.URI), nil
	})
}
