package lsp

import (
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
)

// TestConfigFileIncludePaths verifies that include paths from a workspace
// folder's thrift-ls.json flow through view creation into the snapshot's
// resolver. Paths are resolved relative to the config file, so the
// resolver reaches files that live outside the workspace root.
func TestConfigFileIncludePaths(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()

		dir := t.TempDir()
		includeDir := filepath.Join(dir, "base")
		require.NoError(t, os.MkdirAll(includeDir, 0o755))
		shared := filepath.Join(includeDir, "shared.thrift")
		require.NoError(t, os.WriteFile(shared, []byte("struct Shared {}"), 0o644))
		writeConfig(t, dir, `{"includePaths": ["base"]}`)

		srv := NewServer(cache.New(), nil, Options{})
		_, err := srv.Initialize(ctx, &protocol.InitializeParams{
			WorkspaceFoldersInitializeParams: protocol.WorkspaceFoldersInitializeParams{
				WorkspaceFolders: protocol.NewNullable([]protocol.WorkspaceFolder{{URI: uri.File(dir)}}),
			},
		})
		require.NoError(t, err)
		require.NoError(t, srv.Initialized(ctx, &protocol.InitializedParams{}))

		// The workspace walk runs asynchronously on Initialized; wait for
		// it so the view exists before resolving.
		synctest.Wait()

		app := uri.File(filepath.Join(dir, "app.thrift"))
		view, err := srv.session.ViewOf(app)
		require.NoError(t, err)

		snapshot, release := view.Snapshot()
		defer release()

		// The config's include path is absolute, resolved against the
		// config file's directory, not the process CWD.
		assert.Equal(t, []string{includeDir}, snapshot.Resolver().IncludePaths())

		// Resolving an include that only exists in the configured include
		// path finds it there.
		resolved := snapshot.Resolver().ResolveInclude(app, "shared.thrift")
		assert.Equal(t, uri.File(shared), resolved)
	})
}

// writeConfig writes a thrift-ls.json config file into dir.
func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "thrift-ls.json"), []byte(content), 0o644))
}
