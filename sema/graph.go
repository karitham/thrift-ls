package sema

import (
	"context"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/store"
)

// Graph is the read surface analysis needs from the document store: parse
// files and walk the include graph. Analyzers, fixers, and providers see
// only this; the store's write and concurrency surface (Update, Evict,
// generations) stays with the session owner, except for the batch fixer
// below.
type Graph interface {
	Parse(ctx context.Context, file uri.URI) (*store.ParsedFile, error)
	Dependents(file uri.URI) []uri.URI
	Includers(file uri.URI) []uri.URI
	KnownFiles() []uri.URI
	Folder() uri.URI
	WalkFiles(ctx context.Context, root uri.URI, fn func(uri.URI) error) error
	Resolver() *store.Resolver
}

// Store is the read-write surface batch fixing needs: analyze through
// Graph, land each pass through Update.
type Store interface {
	Graph
	Update(ctx context.Context, changes ...*store.FileChange) store.ChangeResult
}

var (
	_ Graph = (*store.View)(nil)
	_ Store = (*store.View)(nil)
)
