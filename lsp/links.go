package lsp

import (
	"context"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/source"
)

func (s *Server) documentLink(ctx context.Context, params *protocol.DocumentLinkParams) ([]protocol.DocumentLink, error) {
	return withSnapshot(s.session, params.TextDocument.URI, func(ss *cache.Snapshot) ([]protocol.DocumentLink, error) {
		return source.Links(ctx, ss, params.TextDocument.URI), nil
	})
}
