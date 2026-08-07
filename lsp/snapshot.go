package lsp

import (
	"context"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
)

// withSnapshot resolves file's view, acquires its snapshot, and runs fn
// while the snapshot is held. Every request handler funnels through this
// helper so the acquire/release discipline lives in one place.
func withSnapshot[T any](ctx context.Context, session *cache.Session, file uri.URI, fn func(*cache.Snapshot) (T, error)) (T, error) {
	view, err := session.ViewOf(file)
	if err != nil {
		var zero T

		return zero, err
	}

	ss, release := view.Snapshot()
	defer release()

	return fn(ss)
}

// withFile is withSnapshot plus the file handle for file.
func withFile[T any](ctx context.Context, session *cache.Session, file uri.URI, fn func(*cache.Snapshot, cache.FileHandle) (T, error)) (T, error) {
	return withSnapshot(ctx, session, file, func(ss *cache.Snapshot) (T, error) {
		fh, err := ss.ReadFile(ctx, file)
		if err != nil {
			var zero T

			return zero, err
		}

		return fn(ss, fh)
	})
}
