package lsp

import (
	"context"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/store"
	"github.com/karitham/thrift-ls/vfs"
)

type viewResolver func(uri.URI) (*store.View, error)

// withView resolves file's view and runs fn with it. Every request handler
// funnels through this helper so view routing lives in one place.
func withView[T any](resolve viewResolver, file uri.URI, fn func(*store.View) (T, error)) (T, error) {
	view, err := resolve(file)
	if err != nil {
		var zero T

		return zero, err
	}

	return fn(view)
}

// withFile is withView plus the file handle for file.
func withFile[T any](ctx context.Context, resolve viewResolver, file uri.URI, fn func(*store.View, vfs.FileHandle) (T, error)) (T, error) {
	return withView(resolve, file, func(view *store.View) (T, error) {
		fh, err := view.ReadFile(ctx, file)
		if err != nil {
			var zero T

			return zero, err
		}

		return fn(view, fh)
	})
}

func (s *Server) viewOf(file uri.URI) (*store.View, error) {
	return s.workspace.viewOf(file)
}
