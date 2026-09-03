package store

import (
	"context"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/resolver"
)

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

// Checker tests file existence by OS path, without reading content.
// It is the existence half of the resolving system: the include resolver
// probes candidates through it, and frontends with a custom file layout
// (build systems, virtual trees) plug in by serving it from their graph.
// A FileSource that also implements Checker gets cheap probes for free.
type Checker = resolver.Checker

// A FileSource maps URIs to FileHandles. It is the only filesystem seam:
// disk in production, in-memory in tests, build-system backed internally.
// A source may optionally implement Checker (Exists by OS path) to answer
// include probes without reading file bodies; sources that don't get a
// ReadFile fallback.
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
