package source

import (
	"context"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
)

// Batch is the shared analysis context for one diagnostic run. All checkers
// see the same view and the same cross-file resolver, so a name resolved by
// one check is not resolved again by the next.
type Batch struct {
	view *cache.View
	ix   *Index
}

// NewBatch starts an analysis run over the view.
func NewBatch(view *cache.View) *Batch {
	return &Batch{view: view}
}

// View returns the store view this run reads from.
func (b *Batch) View() *cache.View {
	return b.view
}

// Tree returns the parsed tree of uri, parsed and memoized by the view.
func (b *Batch) Tree(ctx context.Context, u uri.URI) (*cache.ParsedFile, error) {
	return b.view.Parse(ctx, u)
}

// Index returns the run's shared cross-file resolver. Resolutions are
// memoized per (file, name), so repeated lookups across checkers resolve
// once.
func (b *Batch) Index() *Index {
	if b.ix == nil {
		b.ix = NewIndex(b.view)
	}

	return b.ix
}
