package lsp

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime/debug"
	"strings"

	"go.lsp.dev/uri"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/source"
)

func (s *Server) didOpen(ctx context.Context, params *protocol.DidOpenTextDocumentParams) error {
	document := params.TextDocument
	if document.LanguageID != LanguageIDThrift {
		return nil
	}

	fileURI := document.URI
	change := &cache.FileChange{
		URI:     fileURI,
		Version: int(document.Version),
		Content: []byte(document.Text),
		From:    cache.FileChangeTypeDidOpen,
	}

	// Opening a file no project owns loads its directory as a folder, so
	// single-file sessions and files outside the workspace resolve. Files
	// under a known root skip this: they join their root through the open
	// document instead of splitting off a spurious inner project. Custom
	// loaders own their files exclusively and never take this path.
	if s.workspace.implicitFolders && !s.workspace.owns(fileURI) && !s.workspace.rootContains(fileURI) {
		s.workspace.loadSync(ctx, []uri.URI{uri.File(filepath.Dir(fileURI.FsPath()))})
	}

	return s.applyChanges(ctx, []*cache.FileChange{change}, true)
}

func (s *Server) didChange(ctx context.Context, params *protocol.DidChangeTextDocumentParams) error {
	return s.applyChanges(ctx, FileChangeFromLSPDidChange(params), true)
}

func (s *Server) didClose(ctx context.Context, params *protocol.DidCloseTextDocumentParams) error {
	fileURI := params.TextDocument.URI
	change := &cache.FileChange{URI: fileURI, From: cache.FileChangeTypeDidClose}

	return s.applyChanges(ctx, []*cache.FileChange{change}, true)
}

// applyChanges is the single path from file events to overlays and views.
// Files route only through snapshot ownership: unowned files update the
// overlay but reach no view.
func (s *Server) applyChanges(ctx context.Context, changes []*cache.FileChange, overlay bool) error {
	if len(changes) == 0 {
		return nil
	}

	for _, change := range changes {
		if change.From == cache.FileChangeTypeDidClose {
			s.forgetReport(change.URI)
		}
	}

	return s.workspace.applyChanges(ctx, changes, overlay)
}

func (s *Server) didChangeWatchedFiles(ctx context.Context, params *protocol.DidChangeWatchedFilesParams) error {
	var changes []*cache.FileChange

	for _, event := range params.Changes {
		if !s.workspace.owns(event.URI) {
			continue
		}

		if s.session.HasOverlay(event.URI) {
			// The editor overlay is authoritative for open documents; disk
			// events for them are ignored.
			continue
		}

		change, err := s.watchedFileChange(ctx, event)
		if err != nil {
			return err
		}

		changes = append(changes, change)
	}

	return s.applyChanges(ctx, changes, false)
}

// watchedFileChange builds a FileChange from a disk event, reading the
// current content through the memoized file source. Deleted files are
// reported as a close change.
func (s *Server) watchedFileChange(ctx context.Context, event protocol.FileEvent) (*cache.FileChange, error) {
	if event.Type == protocol.FileChangeTypeDeleted {
		return &cache.FileChange{URI: event.URI, From: cache.FileChangeTypeDidClose}, nil
	}

	fh, err := s.session.ReadFile(ctx, event.URI)
	if err != nil {
		return nil, err
	}

	content, err := fh.Content()
	if err != nil {
		return nil, err
	}

	return &cache.FileChange{
		URI:     event.URI,
		Version: int(fh.Version()),
		Content: content,
		From:    cache.FileChangeTypeDidChange,
	}, nil
}

// postDiagnostics publishes diagnostics for the affected files of a change
// in a background goroutine, so diagnostics-heavy work never stalls the
// editor request thread. If a newer change lands while the analysis runs,
// the generation check drops the results — the newer change publishes its
// own.
func (s *Server) postDiagnostics(ctx context.Context, view *cache.View, res cache.ChangeResult) {
	if s.diagSync {
		s.diagnoseAt(context.WithoutCancel(ctx), view, res.Affected, res.Gen)

		return
	}

	// The request context dies when the LSP request returns; the
	// diagnostics goroutine outlives it.
	ctx = context.WithoutCancel(ctx)

	go func() {
		// The process must survive a panicking checker: an editor session
		// dying on one bad document is the failure mode this package
		// works hardest to avoid. DebugHandler covers request handlers;
		// this goroutine has no such wrapper of its own.
		defer func() {
			if r := recover(); r != nil {
				slog.Error("diagnostics worker panicked", "panic", r, "stack", string(debug.Stack()))
			}
		}()

		if !view.IsCurrent(res.Gen) {
			return
		}

		s.diagnoseAt(ctx, view, res.Affected, res.Gen)
	}()
}

func (s *Server) completion(ctx context.Context, params *protocol.CompletionParams) (*protocol.CompletionList, error) {
	return withFile(ctx, s.viewOf, params.TextDocument.URI, func(view *cache.View, fh cache.FileHandle) (*protocol.CompletionList, error) {
		items, rng, truncated, err := source.DefaultTokenCompletion.Completion(ctx, view, &source.CompletionRequest{
			Pos: protocol.Position{
				Line:      params.Position.Line,
				Character: params.Position.Character,
			},
			Fh: fh,
		})
		if err != nil {
			return nil, err
		}

		return toLspCompletionList(items, rng, truncated), nil
	})
}

func toLspCompletionList(items []*source.CompletionItem, rng protocol.Range, truncated bool) *protocol.CompletionList {
	list := &protocol.CompletionList{
		IsIncomplete: truncated,
	}

	for i := range items {
		item := protocol.CompletionItem{
			Label:  items[i].Label,
			Detail: protocol.NewOptional(items[i].Detail),
			Kind:   items[i].Kind,
			TextEdit: &protocol.TextEdit{
				NewText: items[i].InsertText,
				Range:   rng,
			},
			FilterText:       protocol.NewOptional(strings.TrimLeft(items[i].Label, "&*")),
			InsertTextFormat: items[i].InsertTextFormat,
			SortText:         protocol.NewOptional(fmt.Sprintf("%05d", i)),
			Preselect:        protocol.NewOptional(i == 0),
			Deprecated:       protocol.NewOptional(items[i].Deprecated),
			Documentation:    protocol.String(items[i].Documentation),
		}
		list.Items = append(list.Items, item)
	}

	return list
}
