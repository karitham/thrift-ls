package vfs

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.lsp.dev/uri"
)

// A memFS is an in-memory file source seeded with URI → content. It keeps
// tests off the real disk: reads and walks resolve against the map, so a
// test can use file:///tmp/... URIs without touching /tmp.
type memFS struct {
	files map[uri.URI][]byte
}

// NewMemFS returns a FileSource backed by files; nil is an empty tree.
func NewMemFS(files map[uri.URI][]byte) FileSource {
	return &memFS{files: files}
}

func (m *memFS) ReadFile(_ context.Context, u uri.URI) (FileHandle, error) {
	content, ok := m.files[u]
	if !ok {
		return &DiskFile{uri: u, err: os.ErrNotExist}, nil
	}

	return &DiskFile{uri: u, content: content}, nil
}

// Exists reports whether path is a seeded file, without reading content.
func (m *memFS) Exists(ctx context.Context, path string) bool {
	if err := ctx.Err(); err != nil {
		return false
	}

	_, ok := m.files[uri.File(path)]

	return ok
}

// WalkFiles calls fn for every seeded file under root, in lexical order.
func (m *memFS) WalkFiles(_ context.Context, root uri.URI, fn func(uri.URI) error) error {
	rootPath := filepath.Clean(root.Path())

	uris := make([]uri.URI, 0, len(m.files))
	for u := range m.files {
		if underRoot(u, rootPath) {
			uris = append(uris, u)
		}
	}
	slices.Sort(uris)

	for _, u := range uris {
		if err := fn(u); err != nil {
			return err
		}
	}

	return nil
}

// underRoot reports whether file is inside rootPath (a directory path):
// same path is out (it is the root itself), and siblings sharing a name
// prefix (e.g. /tmp/ab for root /tmp/a) do not count.
func underRoot(file uri.URI, rootPath string) bool {
	p := file.Path()
	if p == rootPath {
		return false
	}

	return strings.HasPrefix(p, rootPath+"/")
}
