package lsp

import (
	"context"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/source"
)

func (s *Server) semanticTokensFull(ctx context.Context, params *protocol.SemanticTokensParams) (*protocol.SemanticTokens, error) {
	return withView(s.viewOf, params.TextDocument.URI, func(view *cache.View) (*protocol.SemanticTokens, error) {
		data, err := source.Tokens(ctx, view, params.TextDocument.URI)
		if err != nil {
			return nil, err
		}

		return &protocol.SemanticTokens{Data: data}, nil
	})
}
