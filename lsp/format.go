package lsp

import (
	"context"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/source"
)

func (s *Server) formatting(ctx context.Context, params *protocol.DocumentFormattingParams) (result []protocol.TextEdit, err error) {
	return withFile(ctx, s.session, params.TextDocument.URI, func(ss *cache.Snapshot, fh cache.FileHandle) ([]protocol.TextEdit, error) {
		edit, err := source.FormatDocument(ctx, ss, fh, s.formatOptions(ss.View()))
		if err != nil {
			return nil, err
		}

		if edit == nil {
			return nil, nil
		}

		return []protocol.TextEdit{*edit}, nil
	})
}

func (s *Server) rangeFormatting(ctx context.Context, params *protocol.DocumentRangeFormattingParams) (result []protocol.TextEdit, err error) {
	return withFile(ctx, s.session, params.TextDocument.URI, func(ss *cache.Snapshot, fh cache.FileHandle) ([]protocol.TextEdit, error) {
		return source.FormatRange(ctx, ss, fh, s.formatOptions(ss.View()), params.Range)
	})
}
