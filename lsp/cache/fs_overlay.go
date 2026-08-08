package cache

import (
	"context"
	"log/slog"
	"sync"

	"go.lsp.dev/uri"
)

// An overlayFS is a source.FileSource that keeps track of overlays on top of a
// delegate FileSource.
type overlayFS struct {
	delegate FileSource

	mu       sync.Mutex
	overlays map[uri.URI]*Overlay
}

func NewOverlayFS(delegate FileSource) *overlayFS {
	return &overlayFS{
		delegate: delegate,
		overlays: make(map[uri.URI]*Overlay),
	}
}

func (fs *overlayFS) ReadFile(ctx context.Context, uri uri.URI) (FileHandle, error) {
	slog.Debug("reading uri", "uri", uri)
	fs.mu.Lock()
	overlay, ok := fs.overlays[uri]
	fs.mu.Unlock()

	if ok {
		return overlay, nil
	}

	return fs.delegate.ReadFile(ctx, uri)
}

// Update applies changes to the overlay set. DidClose changes remove the
// overlay; all other types create or replace it.
func (fs *overlayFS) Update(_ context.Context, changes []*FileChange) error {
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
func (fs *overlayFS) HasOverlay(uri uri.URI) bool {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	_, ok := fs.overlays[uri]

	return ok
}

// Forget drops the overlay for uri, falling back to disk content.
func (fs *overlayFS) Forget(uri uri.URI) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	delete(fs.overlays, uri)
}

// An Overlay is a file open in the editor. It may have unsaved edits.
// It implements the source.FileHandle interface.
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
