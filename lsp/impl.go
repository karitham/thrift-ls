package lsp

import (
	"context"
	"fmt"
	"path"
	"strings"
	"sync"

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

	s.dirWalkOnce.Do(func() {
		file := change.URI

		dirPos := strings.LastIndexByte(string(file), '/')
		if dirPos == -1 {
			return
		}

		dir := file[0:dirPos]
		s.walkFoldersThriftFile(dir)
	})

	return s.openFile(ctx, change)
}

func (s *Server) openFile(ctx context.Context, change *cache.FileChange) error {
	if change.From != cache.FileChangeTypeInitialize {
		if err := s.session.UpdateOverlayFS(ctx, []*cache.FileChange{change}); err != nil {
			return err
		}
	}

	// The file's directory becomes the view when no workspace folder
	// covers it (single-file mode); AddView dedups and is concurrency-safe.
	view, err := s.session.ViewOf(change.URI)
	if err != nil {
		filename := change.URI.Path()
		view = s.addFolderView(uri.File(path.Dir(filename)))
	}

	view.FileChange(ctx, []*cache.FileChange{change}, s.postDiagnostics(ctx, view))

	return nil
}

func (s *Server) didChange(ctx context.Context, params *protocol.DidChangeTextDocumentParams) error {
	changes := cache.FileChangeFromLSPDidChange(params)
	if err := s.session.UpdateOverlayFS(ctx, changes); err != nil {
		return err
	}

	document := params.TextDocument
	fileURI := document.URI

	view, err := s.session.ViewOf(fileURI)
	if err != nil {
		return err
	}

	view.FileChange(ctx, changes, s.postDiagnostics(ctx, view))

	return nil
}

func (s *Server) didClose(ctx context.Context, params *protocol.DidCloseTextDocumentParams) error {
	fileURI := params.TextDocument.URI

	view, err := s.session.ViewOf(fileURI)
	if err != nil {
		return err
	}

	change := &cache.FileChange{URI: fileURI, From: cache.FileChangeTypeDidClose}

	if err := s.session.UpdateOverlayFS(ctx, []*cache.FileChange{change}); err != nil {
		return err
	}

	view.FileChange(ctx, []*cache.FileChange{change}, s.postDiagnostics(ctx, view))

	return nil
}

func (s *Server) didChangeWatchedFiles(ctx context.Context, params *protocol.DidChangeWatchedFilesParams) error {
	byView := make(map[*cache.View][]*cache.FileChange)

	for _, event := range params.Changes {
		if s.session.HasOverlay(event.URI) {
			// The editor overlay is authoritative for open documents; disk
			// events for them are ignored.
			continue
		}

		change, err := s.watchedFileChange(ctx, event)
		if err != nil {
			return err
		}

		view, err := s.session.ViewOf(event.URI)
		if err != nil {
			continue
		}

		byView[view] = append(byView[view], change)
	}

	for view, changes := range byView {
		view.FileChange(ctx, changes, s.postDiagnostics(ctx, view))
	}

	return nil
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

// postDiagnostics returns a FileChange postFn that publishes diagnostics for
// every affected file (changed files plus their transitive dependents).
// Diagnostics run in the background (FileChange invokes postFns
// asynchronously); if a newer change lands while the analysis runs, the
// generation check drops the results — the newer change publishes its own.
func (s *Server) postDiagnostics(ctx context.Context, view *cache.View) func(uint64, []uri.URI) {
	// The request context dies when the LSP request returns; the
	// diagnostics goroutine outlives it.
	ctx = context.WithoutCancel(ctx)

	return func(gen uint64, affected []uri.URI) {
		if !view.IsCurrent(gen) {
			return
		}

		s.diagnose(ctx, view, affected)
	}
}

// diagnose publishes diagnostics for every affected file, in parallel: a
// change to a shared include re-diagnoses all its dependents, and the view
// (and the client connection) are safe for concurrent reads and
// notifications.
func (s *Server) diagnose(ctx context.Context, view *cache.View, affected []uri.URI) {
	var wg sync.WaitGroup

	for _, file := range affected {
		wg.Go(func() {
			if err := s.diagnostic(ctx, view, file); err != nil {
				logError("diagnostic error", err)
			}
		})
	}

	wg.Wait()
}

func (s *Server) completion(ctx context.Context, params *protocol.CompletionParams) (*protocol.CompletionList, error) {
	return withFile(ctx, s.session, params.TextDocument.URI, func(view *cache.View, fh cache.FileHandle) (*protocol.CompletionList, error) {
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
