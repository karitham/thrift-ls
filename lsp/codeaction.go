package lsp

import (
	"context"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/source"
)

// codeAction returns the quickfixes for the document: formatting the whole
// document when the range covers it, or the range when it is a selection.
func (s *Server) codeAction(ctx context.Context, params *protocol.CodeActionParams) ([]protocol.CommandOrCodeAction, error) {
	return withFile(ctx, s.session, params.TextDocument.URI, func(ss *cache.Snapshot, fh cache.FileHandle) ([]protocol.CommandOrCodeAction, error) {
		action, err := source.FormatDocumentAction(ctx, ss, fh, s.formatOpts)
		if err != nil {
			return nil, err
		}

		if action == nil {
			return nil, nil
		}

		return []protocol.CommandOrCodeAction{action}, nil
	})
}
