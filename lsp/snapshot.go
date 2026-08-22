package lsp

import (
	"context"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
)

// withView resolves file's view and runs fn with it. Every request handler
// funnels through this helper so view routing lives in one place.
func withView[T any](session *cache.Session, file uri.URI, fn func(*cache.View) (T, error)) (T, error) {
	view, err := session.ViewOf(file)
	if err != nil {
		var zero T

		return zero, err
	}

	return fn(view)
}

// withFile is withView plus the file handle for file.
func withFile[T any](ctx context.Context, session *cache.Session, file uri.URI, fn func(*cache.View, cache.FileHandle) (T, error)) (T, error) {
	return withView(session, file, func(view *cache.View) (T, error) {
		fh, err := view.ReadFile(ctx, file)
		if err != nil {
			var zero T

			return zero, err
		}

		return fn(view, fh)
	})
}
