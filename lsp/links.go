package lsp

import (
	"context"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/source"
	"github.com/karitham/thrift-ls/store"
)

func (s *Server) documentLink(ctx context.Context, params *protocol.DocumentLinkParams) ([]protocol.DocumentLink, error) {
	return withView(s.viewOf, params.TextDocument.URI, func(view *store.View) ([]protocol.DocumentLink, error) {
		return source.Links(ctx, view, params.TextDocument.URI), nil
	})
}
