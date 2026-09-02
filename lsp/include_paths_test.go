package lsp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/options"
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

		srv := NewServer(cache.NewMemoizedFS(), nil, Options{})
		_, err := srv.Initialize(ctx, testInitializeParams([]protocol.WorkspaceFolder{{URI: uri.File(dir)}}))
		require.NoError(t, err)
		require.NoError(t, srv.Initialized(ctx, &protocol.InitializedParams{}))

		// The workspace walk runs asynchronously on Initialized; wait for
		// it so the view exists before resolving.
		synctest.Wait()

		app := uri.File(filepath.Join(dir, "app.thrift"))
		view, err := srv.session.ViewOf(app)
		require.NoError(t, err)

		// The config's include path is absolute, resolved against the
		// config file's directory, not the process CWD.
		assert.Equal(t, []string{includeDir}, view.Resolver().IncludePaths())

		// Resolving an include that only exists in the configured include
		// path finds it there.
		resolved := view.Resolver().ResolveInclude(app, "shared.thrift")
		assert.Equal(t, uri.File(shared), resolved)
	})
}

func TestCustomProjectIncludePathsAreAuthoritative(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		root := filepath.Join(dir, "project")
		projectIncludes := filepath.Join(dir, "project-includes")
		configIncludes := filepath.Join(dir, "config-includes")
		cliIncludes := filepath.Join(dir, "cli-includes")
		settingsIncludes := filepath.Join(dir, "settings-includes")
		for _, path := range []string{root, projectIncludes, configIncludes, cliIncludes, settingsIncludes} {
			require.NoError(t, os.MkdirAll(path, 0o755))
		}

		configPath := filepath.Join(root, "thrift-ls.json")
		require.NoError(t, os.WriteFile(configPath, []byte(`{"includePaths":["`+configIncludes+`"]}`), 0o644))
		target := uri.File(filepath.Join(root, "api.thrift"))
		projectDependency := uri.File(filepath.Join(projectIncludes, "shared.thrift"))
		loader := func(context.Context, uri.URI) (WorkspaceSnapshot, error) {
			return WorkspaceSnapshot{Projects: []Project{{
				ConfigURI:    uri.File(filepath.Join(root, "tbuild.yaml")),
				RootURI:      uri.File(root),
				TargetFiles:  []uri.URI{target},
				IncludePaths: []string{projectIncludes},
			}}}, nil
		}
		srv := NewServer(cache.NewMemFS(map[uri.URI][]byte{
			target:            []byte(`include "shared.thrift"`),
			projectDependency: []byte("struct Shared {}"),
		}), nil, Options{
			CLI:             options.Patch{IncludePaths: &[]string{cliIncludes}},
			ConfigFinder:    func(string) (string, error) { return configPath, nil },
			WorkspaceLoader: loader,
		})
		params := testInitializeParams([]protocol.WorkspaceFolder{{URI: uri.File(dir)}})
		params.InitializationOptions = protocol.LSPAny([]byte(`{"includePaths":["` + settingsIncludes + `"]}`))

		_, err := srv.Initialize(t.Context(), params)
		require.NoError(t, err)
		require.NoError(t, srv.Initialized(t.Context(), &protocol.InitializedParams{}))
		synctest.Wait()

		view, err := srv.session.ViewOf(target)
		require.NoError(t, err)
		assert.Equal(t, []string{projectIncludes}, view.Resolver().IncludePaths())
		assert.Equal(t, projectDependency, view.Resolver().ResolveInclude(target, "shared.thrift"))
	})
}

// writeConfig writes a thrift-ls.json config file into dir.
func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "thrift-ls.json"), []byte(content), 0o644))
}
