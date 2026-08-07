package lsp

import (
	"context"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/semantic"
)

func (s *Server) semanticTokensFull(ctx context.Context, params *protocol.SemanticTokensParams) (*protocol.SemanticTokens, error) {
	file := params.TextDocument.URI

	view, err := s.session.ViewOf(file)
	if err != nil {
		return nil, err
	}

	ss, release := view.Snapshot()
	defer release()

	data, err := semantic.Tokens(ctx, ss, file)
	if err != nil {
		return nil, err
	}

	return &protocol.SemanticTokens{Data: data}, nil
}
