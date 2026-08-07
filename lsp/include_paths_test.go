package lsp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/formatter"
	"github.com/karitham/thrift-ls/lsp/cache"
)

// TestServerIncludePathsFlow verifies that include paths configured on the
// server's cache flow through the session, view, and snapshot into the
// resolver.
func TestServerIncludePathsFlow(t *testing.T) {
	dir := t.TempDir()
	includeDir := filepath.Join(dir, "base")
	assert.NoError(t, os.MkdirAll(includeDir, 0o755))
	shared := filepath.Join(includeDir, "shared.thrift")
	assert.NoError(t, os.WriteFile(shared, []byte("struct Shared {}"), 0o644))

	c := cache.New([]string{includeDir})
	srv := NewServer(c, nil, formatter.DefaultOptions())

	// Views are created per workspace folder at initialization.
	srv.session.AddView(uri.File(dir))
	view, err := srv.session.ViewOf(uri.File(filepath.Join(dir, "app.thrift")))
	assert.NoError(t, err)

	snapshot, release := view.Snapshot()
	defer release()

	// Resolving an include that only exists in the configured include path
	// finds it there.
	resolved := snapshot.Resolver().ResolveInclude(uri.File(filepath.Join(dir, "app.thrift")), "shared.thrift")
	assert.Equal(t, uri.File(shared), resolved)

	// The snapshot exposes the configured paths.
	assert.Equal(t, []string{includeDir}, snapshot.Resolver().IncludePaths())
}
