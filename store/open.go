package store

import (
	"context"
	"os"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/vfs"
)

// OpenDisk opens a session over the memoized disk source with files open
// in the overlay of a view rooted at folder, and returns the session
// (needed to push new content into the overlay later), its view, and the
// files' URIs. It is the non-editor entry point: check, dump, and other
// CLI frontends share it instead of hand-rolling sessions.
func OpenDisk(ctx context.Context, files []string, folder string, includePaths []string) (*Session, *View, []uri.URI, error) {
	sess := NewSession(vfs.NewMemoizedFS())
	view := sess.AddView(uri.File(folder), includePaths)

	changes := make([]*vfs.FileChange, 0, len(files))
	uris := make([]uri.URI, 0, len(files))

	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			return nil, nil, nil, err
		}

		u := uri.File(file)
		uris = append(uris, u)
		changes = append(changes, &vfs.FileChange{URI: u, Version: 0, Content: content, From: vfs.FileChangeTypeDidOpen})
	}

	if err := sess.Update(ctx, changes); err != nil {
		return nil, nil, nil, err
	}

	return sess, view, uris, nil
}
