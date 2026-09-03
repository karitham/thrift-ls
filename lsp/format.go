package lsp

import (
	"context"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/source"
	"github.com/karitham/thrift-ls/store"
	"github.com/karitham/thrift-ls/vfs"
)

func (s *Server) formatting(ctx context.Context, params *protocol.DocumentFormattingParams) (result []protocol.TextEdit, err error) {
	return withFile(ctx, s.viewOf, params.TextDocument.URI, func(view *store.View, fh vfs.FileHandle) ([]protocol.TextEdit, error) {
		edit, err := source.FormatDocument(ctx, view, fh, s.formatOptions(view))
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
	return withFile(ctx, s.viewOf, params.TextDocument.URI, func(view *store.View, fh vfs.FileHandle) ([]protocol.TextEdit, error) {
		return source.FormatRange(ctx, view, fh, s.formatOptions(view), params.Range)
	})
}
