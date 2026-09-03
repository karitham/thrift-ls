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
//
// One failing includer aborts the whole request on purpose: applying
// partial edits would silently leave half the dependents pointing at the
// dead path, while failing the request leaves the rename untouched for
// the user to retry.
func (s *Server) willRenameFiles(ctx context.Context, params *protocol.RenameFilesParams) (*protocol.WorkspaceEdit, error) {
	edit := &protocol.WorkspaceEdit{Changes: make(map[uri.URI][]protocol.TextEdit)}

	for _, f := range params.Files {
		oldURI := uri.URI(f.OldURI)
		newURI := uri.URI(f.NewURI)
		if !strings.HasSuffix(oldURI.Path(), ".thrift") || !strings.HasSuffix(newURI.Path(), ".thrift") {
			continue
		}

		view, err := s.viewOf(oldURI)
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

// didRenameFiles drops the renamed-away URI's state — its editor overlay
// and its store entry — so a stale parse of the old location does not
// survive, and refreshes every direct includer so their include edges
// re-resolve even in clients without working file watchers.
func (s *Server) didRenameFiles(ctx context.Context, params *protocol.RenameFilesParams) error {
	for _, f := range params.Files {
		oldURI := uri.URI(f.OldURI)
		if !strings.HasSuffix(oldURI.Path(), ".thrift") {
			continue
		}

		view, err := s.viewOf(oldURI)
		if err != nil {
			continue
		}

		// Forget the editor overlay first, mirroring didClose: otherwise
		// the stale buffer shadows the file system forever, and the
		// watcher guard drops this rename's delete/create events.
		change := &cache.FileChange{URI: oldURI, From: cache.FileChangeTypeDidClose}
		if err := s.session.Update(ctx, []*cache.FileChange{change}); err != nil {
			return err
		}

		s.postDiagnostics(ctx, view, view.Update(ctx, change))

		s.refreshIncluders(ctx, view, oldURI)
	}

	return nil
}

// refreshIncluders re-feeds every direct includer's current content to the
// store, so their include edges re-resolve after a rename even in clients
// without working file watchers. Overlay-backed includers are re-fed their
// own current content: a cheap no-op re-parse.
func (s *Server) refreshIncluders(ctx context.Context, view *cache.View, oldURI uri.URI) {
	var refreshes []*cache.FileChange

	for _, inc := range view.Includers(oldURI) {
		fh, err := s.session.ReadFile(ctx, inc)
		if err != nil {
			logError("rename refresh failed", err, "uri", inc)

			continue
		}

		content, err := fh.Content()
		if err != nil {
			logError("rename refresh failed", err, "uri", inc)

			continue
		}

		refreshes = append(refreshes, &cache.FileChange{
			URI:     inc,
			Version: int(fh.Version()),
			Content: content,
			From:    cache.FileChangeTypeDidChange,
		})
	}

	if len(refreshes) > 0 {
		s.postDiagnostics(ctx, view, view.Update(ctx, refreshes...))
	}
}
