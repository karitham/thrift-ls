package lsp

import (
	"context"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/source"
	"github.com/karitham/thrift-ls/store"
)

func (s *Server) definition(ctx context.Context, params *protocol.DefinitionParams) (result []protocol.Location, err error) {
	return withView(s.viewOf, params.TextDocument.URI, func(view *store.View) ([]protocol.Location, error) {
		return source.Definition(ctx, view, params.TextDocument.URI, params.Position)
	})
}

func (s *Server) references(ctx context.Context, params *protocol.ReferenceParams) (result []protocol.Location, err error) {
	return withView(s.viewOf, params.TextDocument.URI, func(view *store.View) ([]protocol.Location, error) {
		return source.Reference(ctx, view, params.TextDocument.URI, params.Position)
	})
}

func (s *Server) typeDefinition(ctx context.Context, params *protocol.TypeDefinitionParams) (result []protocol.Location, err error) {
	return withView(s.viewOf, params.TextDocument.URI, func(view *store.View) ([]protocol.Location, error) {
		return source.TypeDefinition(ctx, view, params.TextDocument.URI, params.Position)
	})
}
