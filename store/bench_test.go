package store

import (
	"context"
	"fmt"
	"testing"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/vfs"
)

// benchChain builds a linear include chain: chain_0 includes chain_1, ...
// Content lives in overlays, so the benchmark exercises store logic only —
// no disk access.
func benchChain(b *testing.B, n int) (*View, []*vfs.FileChange) {
	b.Helper()

	files := make([]*vfs.FileChange, 0, n)
	for i := range n {
		content := fmt.Sprintf("include \"chain_%d.thrift\"\n\nstruct S%d {\n\t1: required string Name\n}\n", i+1, i)
		if i == n-1 {
			content = fmt.Sprintf("struct S%d {\n\t1: required string Name\n}\n", i)
		}

		files = append(files, &vfs.FileChange{
			URI:     uri.File(fmt.Sprintf("/tmp/chain_%d.thrift", i)),
			Version: 0,
			Content: []byte(content),
			From:    vfs.FileChangeTypeDidOpen,
		})
	}

	fs := vfs.NewOverlayFS(vfs.NewMemoizedFS())
	if err := fs.Update(context.Background(), files); err != nil {
		b.Fatal(err)
	}

	view := NewView("file:///tmp", fs, nil)

	for _, f := range files {
		if _, err := view.Parse(context.Background(), f.URI); err != nil {
			b.Fatal(err)
		}
	}

	return view, files
}

func BenchmarkStoreParseHit(b *testing.B) {
	view, files := benchChain(b, 100)
	ctx := context.Background()

	b.ResetTimer()

	for b.Loop() {
		for _, f := range files {
			if _, err := view.Parse(ctx, f.URI); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkStoreFileChange changes a mid-chain file each iteration: the
// changed file is invalidated and re-parsed eagerly.
func BenchmarkStoreFileChange(b *testing.B) {
	view, files := benchChain(b, 100)
	ctx := context.Background()
	mid := files[len(files)/2]
	mid.Content = append([]byte(nil), mid.Content...)

	b.ResetTimer()

	for i := 0; b.Loop(); i++ {
		mid.Version = i

		view.Update(ctx, mid)
	}
}

// BenchmarkStoreChangeThenReadAll is the keystroke cycle: change one file,
// then touch every file's parse (as diagnostics across dependents would).
// This is where invalidation strategy shows up in the numbers.
func BenchmarkStoreChangeThenReadAll(b *testing.B) {
	view, files := benchChain(b, 100)
	ctx := context.Background()
	mid := files[len(files)/2]

	b.ResetTimer()

	for i := 0; b.Loop(); i++ {
		mid.Version = i
		mid.Content = fmt.Appendf(nil, "include \"chain_%d.thrift\"\n\nstruct S%d {\n\t1: required string Name\n}\n",
			len(files)/2+1, len(files)/2)

		view.Update(ctx, mid)

		for _, f := range files {
			if _, err := view.Parse(ctx, f.URI); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkStoreDependents(b *testing.B) {
	view, files := benchChain(b, 100)
	leaf := files[len(files)-1].URI // the bottom of the chain: everyone includes it transitively

	b.ResetTimer()

	for b.Loop() {
		if deps := view.Dependents(leaf); len(deps) == 0 {
			b.Fatal("expected dependents")
		}
	}
}
