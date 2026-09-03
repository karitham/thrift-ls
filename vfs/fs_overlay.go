package vfs

import (
	"context"
	"log/slog"
	"sync"

	"go.lsp.dev/uri"
)

// An OverlayFS is a FileSource that keeps track of overlays on top of a
// delegate FileSource.
type OverlayFS struct {
	delegate FileSource

	mu       sync.Mutex
	overlays map[uri.URI]*Overlay
}

func NewOverlayFS(delegate FileSource) *OverlayFS {
	return &OverlayFS{
		delegate: delegate,
		overlays: make(map[uri.URI]*Overlay),
	}
}

func (fs *OverlayFS) ReadFile(ctx context.Context, uri uri.URI) (FileHandle, error) {
	slog.Debug("reading uri", "uri", uri)
	fs.mu.Lock()
	overlay, ok := fs.overlays[uri]
	fs.mu.Unlock()

	if ok {
		return overlay, nil
	}

	return fs.delegate.ReadFile(ctx, uri)
}

// Exists reports whether path names an open overlay or an existing delegate
// file, without reading content. Open files count even when the disk copy
// is missing, so includes resolve for unsaved buffers.
func (fs *OverlayFS) Exists(ctx context.Context, path string) bool {
	if err := ctx.Err(); err != nil {
		return false
	}

	u := uri.File(path)

	fs.mu.Lock()
	_, ok := fs.overlays[u]
	fs.mu.Unlock()

	if ok {
		return true
	}

	if ex, ok := fs.delegate.(Checker); ok {
		return ex.Exists(ctx, path)
	}

	fh, err := fs.delegate.ReadFile(ctx, u)
	if err != nil {
		return false
	}

	_, err = fh.Content()

	return err == nil
}

// WalkFiles enumerates the delegate's tree, not the overlay: the walk
// discovers files on the underlying source, while open files are already
// known to the session via their didOpen.
func (fs *OverlayFS) WalkFiles(ctx context.Context, root uri.URI, fn func(uri.URI) error) error {
	return fs.delegate.WalkFiles(ctx, root, fn)
}

// Update applies changes to the overlay set. DidClose changes remove the
// overlay; all other types create or replace it.
func (fs *OverlayFS) Update(_ context.Context, changes []*FileChange) error {
	for _, change := range changes {
		if change.From == FileChangeTypeDidClose {
			fs.Forget(change.URI)

			continue
		}

		overlay := NewOverlay(change.URI, change.FullContent(), int32(change.Version))

		slog.Debug("new overlay content", "content", string(overlay.content), "uri", change.URI)

		fs.mu.Lock()
		fs.overlays[change.URI] = overlay
		fs.mu.Unlock()
	}

	return nil
}

// HasOverlay reports whether uri has an open overlay.
func (fs *OverlayFS) HasOverlay(uri uri.URI) bool {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	_, ok := fs.overlays[uri]

	return ok
}

// Forget drops the overlay for uri, falling back to disk content.
func (fs *OverlayFS) Forget(uri uri.URI) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	delete(fs.overlays, uri)
}

// An Overlay is a file open in the editor. It may have unsaved edits.
// It implements the FileHandle interface.
type Overlay struct {
	uri     uri.URI
	content []byte
	version int32
}

func NewOverlay(uri uri.URI, content []byte, version int32) *Overlay {
	return &Overlay{
		uri:     uri,
		content: content,
		version: version,
	}
}

func (o *Overlay) URI() uri.URI { return o.uri }

func (o *Overlay) Content() ([]byte, error) { return o.content, nil }
func (o *Overlay) Version() int32           { return o.version }
