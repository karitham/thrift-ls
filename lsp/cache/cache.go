package cache

import (
	"context"

	"go.lsp.dev/uri"
)

// Cache is the process-wide file store, backed by a FileSource (the disk
// in production, an in-memory tree in tests). Include paths are not
// global: each view resolves its own from its workspace folder's config
// at creation.
type Cache struct {
	fs FileSource
}

// New returns a disk-backed cache.
func New() *Cache {
	return NewWithFS(&memoizedFS{filesByID: map[FileID][]*DiskFile{}})
}

// NewWithFS returns a cache backed by fs, for tests and embedding.
func NewWithFS(fs FileSource) *Cache {
	return &Cache{fs: fs}
}

// ReadFile implements FileSource by delegating to the backing source.
func (c *Cache) ReadFile(ctx context.Context, u uri.URI) (FileHandle, error) {
	return c.fs.ReadFile(ctx, u)
}

// WalkFiles implements FileSource by delegating to the backing source.
func (c *Cache) WalkFiles(ctx context.Context, root uri.URI, fn func(uri.URI) error) error {
	return c.fs.WalkFiles(ctx, root, fn)
}
