package source

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.lsp.dev/uri"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/cache"
)

// buildSnapshot builds a view from file contents with optional include
// paths.
func buildSnapshot(t *testing.T, includePaths []string, files ...*cache.FileChange) *cache.View {
	t.Helper()

	c := cache.NewMemoizedFS()
	fs := cache.NewOverlayFS(c)
	_ = fs.Update(t.Context(), files)
	view := cache.NewView(uri.File("/tmp"), fs, includePaths)

	for _, f := range files {
		_, _ = view.Parse(t.Context(), f.URI)
	}

	return view
}

func TestCompletionEndToEnd(t *testing.T) {
	content := "struct User {\n  1: required i64 id\n}\n\nstruct Profile {\n  1: required Us\n}"
	view := buildSnapshot(t, nil,
		&cache.FileChange{URI: "file:///tmp/test.thrift", Version: 0, Content: []byte(content), From: cache.FileChangeTypeDidOpen},
	)

	fh, err := view.ReadFile(t.Context(), "file:///tmp/test.thrift")
	assert.NoError(t, err)

	cmp := &CompletionRequest{
		Fh:  fh,
		Pos: protocol.Position{Line: 5, Character: 16}, // after "Us" in "1: required Us"
	}
	items, _, _, err := DefaultTokenCompletion.Completion(t.Context(), view, cmp)
	assert.NoError(t, err)

	labels := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, item.Label)
	}
	// The type position completes the struct name from the same file.
	assert.Contains(t, labels, "User")
}
