package lsp

import (
	"context"
	"strings"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/source"
	"github.com/karitham/thrift-ls/store"
)

func (s *Server) hover(ctx context.Context, params *protocol.HoverParams) (*protocol.Hover, error) {
	return withView(s.viewOf, params.TextDocument.URI, func(view *store.View) (*protocol.Hover, error) {
		content, err := source.Hover(ctx, view, params.TextDocument.URI, params.Position)
		if err != nil {
			return nil, err
		}

		if content == "" {
			return nil, nil
		}

		markdownPrefix := "```thrift\n"
		if strings.HasPrefix(content, "\n") {
			markdownPrefix = "```thrift"
		}

		markdownSuffix := "\n```"
		if strings.HasSuffix(content, "\n") {
			markdownSuffix = "```"
		}

		return &protocol.Hover{
			Contents: &protocol.MarkupContent{
				Kind:  protocol.MarkupKindMarkdown,
				Value: markdownPrefix + content + markdownSuffix,
			},
		}, nil
	})
}
