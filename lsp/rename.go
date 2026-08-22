package lsp

import (
	"context"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/source"
)

func (s *Server) prepareRename(ctx context.Context, params *protocol.PrepareRenameParams) (*protocol.Range, error) {
	return withView(s.session, params.TextDocument.URI, func(view *cache.View) (*protocol.Range, error) {
		return source.PrepareRename(ctx, view, params.TextDocument.URI, params.Position)
	})
}

func (s *Server) rename(ctx context.Context, params *protocol.RenameParams) (*protocol.WorkspaceEdit, error) {
	return withView(s.session, params.TextDocument.URI, func(view *cache.View) (*protocol.WorkspaceEdit, error) {
		return source.Rename(ctx, view, params.TextDocument.URI, params.Position, params.NewName)
	})
}
