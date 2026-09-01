package cache

import (
	"context"
	"time"

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

// A FileHandle represents the URI, content, and optional version of a file
// tracked by the LSP session.
//
// File content may be provided by the file system or from an overlay for an
// open file with unsaved edits.
// A FileHandle may record an attempt to read a non-existent file,
// in which case Content returns an error.
type FileHandle interface {
	// URI is the URI for this file handle.
	// TODO(rfindley): this is not actually well-defined. In some cases, there
	// may be more than one URI that resolve to the same FileHandle. Which one is
	// this?
	URI() uri.URI
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
	// WalkFiles calls fn for every file under root, recursively, in
	// lexical order. The caller filters by kind (e.g. extension). An
	// error returned by fn stops the walk; per-entry failures (missing
	// roots, permissions) are the implementation's to skip or report.
	WalkFiles(ctx context.Context, root uri.URI, fn func(uri.URI) error) error
}

type FileChangeType string

const (
	FileChangeTypeInitialize FileChangeType = "Initialize"
	FileChangeTypeDidOpen    FileChangeType = "DidOpen"
	FileChangeTypeDidChange  FileChangeType = "DidChange"
	FileChangeTypeDidClose   FileChangeType = "DidClose"
)

type FileChange struct {
	URI     uri.URI
	Version int
	Content []byte
	From    FileChangeType
}

func (f *FileChange) FullContent() []byte {
	return f.Content
}
