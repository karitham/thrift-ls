package store

import (
	"go.lsp.dev/uri"
)

// OpenDisk opens a session over the disk source with a view rooted at
// folder, and returns the session, its view, and the files' URIs. It is
// the non-editor entry point: check, dump, and other CLI frontends share
// it instead of hand-rolling sessions.
//
// Files are deliberately not placed in the overlay: nothing is open in an
// editor, so parses read straight from disk and every pass observes what
// the previous pass wrote.
func OpenDisk(files []string, folder string, includePaths []string) (*Session, *View, []uri.URI) {
	sess := NewSession(NewDiskFS())
	view := sess.AddView(uri.File(folder), includePaths)

	uris := make([]uri.URI, 0, len(files))
	for _, file := range files {
		uris = append(uris, uri.File(file))
	}

	return sess, view, uris
}
