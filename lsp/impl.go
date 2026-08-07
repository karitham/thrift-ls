package lsp

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/completion"
	"github.com/karitham/thrift-ls/lsp/types"
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

	s.session.Initialize(func() {
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

	if _, err := s.session.ViewOf(change.URI); err != nil {
		// create view for this folder
		filename := change.URI.Path()
		dir := uri.File(path.Dir(filename))
		s.session.CreateView(dir)
	}

	view, _ := s.session.ViewOf(change.URI)
	view.FileChange(ctx, []*cache.FileChange{change}, func() {
		ss, release := view.Snapshot()
		defer release()

		err := s.diagnostic(ctx, ss, change)
		if err != nil {
			slog.Error("diagnostic error", "err", err)
		}
	})

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

	view.FileChange(ctx, changes, func() {
		ss, release := view.Snapshot()
		defer release()

		for i := range changes {
			err := s.diagnostic(ctx, ss, changes[i])
			if err != nil {
				slog.Error("diagnostic error", "err", err)
			}
		}
	})

	return nil
}

func (s *Server) completion(ctx context.Context, params *protocol.CompletionParams) (*protocol.CompletionList, error) {
	snapshot, release, fh, err := s.getFileContext(ctx, params.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	defer release()

	items, rng, err := completion.DefaultTokenCompletion.Completion(ctx, snapshot, &completion.CompletionRequest{
		TriggerKind: 0,
		Pos: types.Position{
			Line:      params.Position.Line,
			Character: params.Position.Character,
		},
		Fh: fh,
	})
	if err != nil {
		return nil, err
	}

	return toLspCompletionList(items, rng), nil
}

func toLspCompletionList(items []*completion.CompletionItem, rng protocol.Range) *protocol.CompletionList {
	list := &protocol.CompletionList{
		IsIncomplete: true,
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

func (s *Server) getFileContext(ctx context.Context, uri uri.URI) (ss *cache.Snapshot, release func(), fh cache.FileHandle, err error) {
	var view *cache.View

	view, err = s.session.ViewOf(uri)
	if err != nil {
		return ss, release, fh, err
	}

	ss, release = view.Snapshot()

	fh, err = ss.ReadFile(ctx, uri)
	if err != nil {
		release()

		return ss, release, fh, err
	}

	return ss, release, fh, err
}
