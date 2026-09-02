package lsp

import (
	"context"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
)

type viewResolver func(uri.URI) (*cache.View, error)

// withView resolves file's view and runs fn with it. Every request handler
// funnels through this helper so view routing lives in one place.
func withView[T any](resolve viewResolver, file uri.URI, fn func(*cache.View) (T, error)) (T, error) {
	view, err := resolve(file)
	if err != nil {
		var zero T

		return zero, err
	}

	return fn(view)
}

// withFile is withView plus the file handle for file.
func withFile[T any](ctx context.Context, resolve viewResolver, file uri.URI, fn func(*cache.View, cache.FileHandle) (T, error)) (T, error) {
	return withView(resolve, file, func(view *cache.View) (T, error) {
		fh, err := view.ReadFile(ctx, file)
		if err != nil {
			var zero T

			return zero, err
		}

		return fn(view, fh)
	})
}

func (s *Server) viewOf(file uri.URI) (*cache.View, error) {
	if s.workspace != nil {
		return s.workspace.viewOf(file)
	}

	return s.session.ViewOf(file)
}
