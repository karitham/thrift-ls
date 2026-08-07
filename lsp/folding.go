package lsp

import (
	"context"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/folding"
)

func (s *Server) foldingRanges(ctx context.Context, params *protocol.FoldingRangeParams) ([]protocol.FoldingRange, error) {
	file := params.TextDocument.URI

	view, err := s.session.ViewOf(file)
	if err != nil {
		return nil, err
	}

	ss, release := view.Snapshot()
	defer release()

	return folding.Ranges(ctx, ss, file), nil
}
