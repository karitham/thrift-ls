package cache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"maps"
	"sync"
	"time"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// A FileID uniquely identifies a file in the file system.
//
// If GetFileID(name1) returns the same ID as GetFileID(name2), the two file
// names denote the same file.
// A FileID is comparable, and thus suitable for use as a map key.
type FileID struct {
	device, inode uint64
}

// GetFileID returns the file system's identifier for the file, and its
// modification time.
// Like os.Stat, it reads through symbolic links.
func GetFileID(filename string) (FileID, time.Time, error) { return getFileID(filename) }

type Hash [sha256.Size]byte

// HashOf returns the hash of some data.
func HashOf(data []byte) Hash {
	return Hash(sha256.Sum256(data))
}

// Hashf returns the hash of a printf-formatted string.
func Hashf(format string, args ...any) Hash {
	// Although this looks alloc-heavy, it is faster than using
	// Fprintf on sha256.New() because the allocations don't escape.
	return HashOf(fmt.Appendf(nil, format, args...))
}

// String returns the digest as a string of hex digits.
func (h Hash) String() string {
	return fmt.Sprintf("%64x", [sha256.Size]byte(h))
}

// Less returns true if the given hash is less than the other.
func (h Hash) Less(other Hash) bool {
	return bytes.Compare(h[:], other[:]) < 0
}

// XORWith updates *h to *h XOR h2.
func (h *Hash) XORWith(h2 Hash) {
	// Small enough that we don't need crypto/subtle.XORBytes.
	for i := range h {
		h[i] ^= h2[i]
	}
}

// FileIdentity uniquely identifies a file at a version from a FileSystem.
type FileIdentity struct {
	URI  uri.URI
	Hash Hash // digest of file contents
}

func (id FileIdentity) String() string {
	return fmt.Sprintf("%s%s", id.URI, id.Hash)
}

// A FileHandle represents the URI, content, hash, and optional
// version of a file tracked by the LSP session.
//
// File content may be provided by the file system (for Saved files)
// or from an overlay, for open files with unsaved edits.
// A FileHandle may record an attempt to read a non-existent file,
// in which case Content returns an error.
type FileHandle interface {
	// URI is the URI for this file handle.
	// TODO(rfindley): this is not actually well-defined. In some cases, there
	// may be more than one URI that resolve to the same FileHandle. Which one is
	// this?
	URI() uri.URI
	// FileIdentity returns a FileIdentity for the file, even if there was an
	// error reading it.
	FileIdentity() FileIdentity
	// Saved reports whether the file has the same content on disk:
	// it is false for files open on an editor with unsaved edits.
	Saved() bool
	// Version returns the file version, as defined by the LSP client.
	// For on-disk file handles, Version returns 0.
	Version() int32
	// Content returns the contents of a file.
	// If the file is not available, returns a nil slice and an error.
	Content() ([]byte, error)
}

// A FileSource maps URIs to FileHandles.
type FileSource interface {
	// ReadFile returns the FileHandle for a given URI, either by
	// reading the content of the file or by obtaining it from a cache.
	ReadFile(ctx context.Context, uri uri.URI) (FileHandle, error)
}

// FilesMap holds files on disk and overlay files. Snapshots share the
// underlying maps (Clone is O(1)); the first write after a clone copies
// copy-on-write.
type FilesMap struct {
	mu       sync.RWMutex
	files    map[uri.URI]FileHandle
	overlays map[uri.URI]*Overlay
	shared   bool
}

func (m *FilesMap) Get(key uri.URI) (FileHandle, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	fh, ok := m.files[key]

	return fh, ok
}

func (m *FilesMap) Set(key uri.URI, file FileHandle) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.copyOnWrite()

	m.files[key] = file
	if o, ok := file.(*Overlay); ok {
		m.overlays[key] = o
	}
}

func (m *FilesMap) Forget(key uri.URI) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.copyOnWrite()

	delete(m.files, key)
	delete(m.overlays, key)
}

// Clone returns a view sharing the same entries. The clone and the original
// both become copy-on-write.
func (m *FilesMap) Clone() *FilesMap {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.shared = true

	return &FilesMap{
		files:    m.files,
		overlays: m.overlays,
		shared:   true,
	}
}

// copyOnWrite detaches the maps from a shared parent before the first write.
// Callers must hold mu.
func (m *FilesMap) copyOnWrite() {
	if !m.shared {
		return
	}

	files := make(map[uri.URI]FileHandle, len(m.files)+1)
	maps.Copy(files, m.files)

	overlays := make(map[uri.URI]*Overlay, len(m.overlays)+1)
	maps.Copy(overlays, m.overlays)

	m.files = files
	m.overlays = overlays
	m.shared = false
}

func (m *FilesMap) Destroy() {
	m.files = nil
	m.overlays = nil
}

type FileChangeType string

const (
	FileChangeTypeInitialize FileChangeType = "Initialize"
	FileChangeTypeDidOpen    FileChangeType = "DidOpen"
	FileChangeTypeDidChange  FileChangeType = "DidChange"
	FileChangeTypeDidSave    FileChangeType = "DidSave"
	FileChangeTypeDidClose   FileChangeType = "DidClose"
)

type FileChange struct {
	URI     uri.URI
	Version int
	Content []byte
	From    FileChangeType
}

func (f *FileChange) FullContent(base []byte) []byte {
	// only support full change now
	return f.Content
}

func FileChangeFromLSPDidChange(params *protocol.DidChangeTextDocumentParams) []*FileChange {
	changes := make([]*FileChange, 0, len(params.ContentChanges))
	for i := range params.ContentChanges {
		event, ok := params.ContentChanges[i].(*protocol.TextDocumentContentChangeWholeDocument)
		if !ok {
			// Incremental changes are not supported; fall back to full reload
			// semantics using the current full content.
			continue
		}

		changes = append(changes, &FileChange{
			URI:     params.TextDocument.URI,
			Version: int(params.TextDocument.Version),
			Content: []byte(event.Text),
			From:    FileChangeTypeDidChange,
		})
	}

	return changes
}
