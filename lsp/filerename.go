package lsp

import (
	"context"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/source"
)

// willRenameFiles computes the edits re-pointing every include of each
// renamed thrift file, so the client applies them together with the
// rename and no include is left dangling.
func (s *Server) willRenameFiles(ctx context.Context, params *protocol.RenameFilesParams) (*protocol.WorkspaceEdit, error) {
	edit := &protocol.WorkspaceEdit{Changes: make(map[uri.URI][]protocol.TextEdit)}

	for _, f := range params.Files {
		oldURI := uri.URI(f.OldURI)
		if !strings.HasSuffix(oldURI.Path(), ".thrift") {
			continue
		}

		view, err := s.session.ViewOf(oldURI)
		if err != nil {
			continue
		}

		changes, err := source.RenameFileEdits(ctx, view, oldURI, uri.URI(f.NewURI))
		if err != nil {
			return nil, err
		}

		for u, texts := range changes {
			edit.Changes[u] = append(edit.Changes[u], texts...)
		}
	}

	if len(edit.Changes) == 0 {
		return nil, nil
	}

	return edit, nil
}

// didRenameFiles drops the renamed-away URI's state, so a stale parse of
// the old location does not survive in clients without working file
// watchers. The new location parses lazily on first request.
func (s *Server) didRenameFiles(ctx context.Context, params *protocol.RenameFilesParams) error {
	for _, f := range params.Files {
		oldURI := uri.URI(f.OldURI)

		view, err := s.session.ViewOf(oldURI)
		if err != nil {
			continue
		}

		change := &cache.FileChange{URI: oldURI, From: cache.FileChangeTypeDidClose}
		s.postDiagnostics(ctx, view, view.Update(ctx, change))
	}

	return nil
}
