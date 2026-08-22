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
	"github.com/karitham/thrift-ls/options"
)

// Probe: printWidth 80 keeps the long struct on one line, 30 breaks it.
const probe = "struct LongName{1: string fieldNameThatIsQuiteLong}\n"

const (
	probeOneLine = "struct LongName { 1: string fieldNameThatIsQuiteLong }\n"
	probeBroken  = "struct LongName {\n    1: string fieldNameThatIsQuiteLong\n}\n"
)

// openAndFormat opens a thrift document and returns its formatted text.
func openAndFormat(t *testing.T, srv *Server, file string) string {
	t.Helper()

	require.NoError(t, srv.DidOpen(t.Context(), &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        uri.File(file),
			LanguageID: "thrift",
			Version:    0,
			Text:       probe,
		},
	}))

	edits, err := srv.Formatting(t.Context(), &protocol.DocumentFormattingParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri.File(file)},
	})
	require.NoError(t, err)
	require.Len(t, edits, 1)

	return edits[0].NewText
}

// initWorkspace runs the initialize handshake for folders, waiting for the
// async workspace walk (synctest.Wait) so the views — and their
// per-folder configs — exist before any file opens. Callers must run
// inside a synctest bubble.
func initWorkspace(t *testing.T, srv *Server, folders []uri.URI, initializationOptions []byte) {
	t.Helper()

	_, err := srv.Initialize(t.Context(), &protocol.InitializeParams{
		WorkspaceFolders:      protocol.NewNullable(foldersFromURIs(folders)),
		InitializationOptions: protocol.LSPAny(initializationOptions),
	})
	require.NoError(t, err)
	require.NoError(t, srv.Initialized(t.Context(), &protocol.InitializedParams{}))

	synctest.Wait()
}

func foldersFromURIs(uris []uri.URI) []protocol.WorkspaceFolder {
	folders := make([]protocol.WorkspaceFolder, 0, len(uris))
	for _, u := range uris {
		folders = append(folders, protocol.WorkspaceFolder{URI: u})
	}

	return folders
}

// TestConfigDiscoveryPerWorkspaceFolder verifies that each workspace
// folder formats with its own thrift-ls.json: no single process-global
// config baked in before the workspace was known.
func TestConfigDiscoveryPerWorkspaceFolder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dirA := t.TempDir()
		dirB := t.TempDir()
		writeConfig(t, dirA, `{"printWidth": 30}`)
		writeConfig(t, dirB, `{"printWidth": 100}`)

		srv := NewServer(cache.NewMemoizedFS(), nil, Options{})
		initWorkspace(t, srv, []uri.URI{uri.File(dirA), uri.File(dirB)}, nil)

		// One server, two folders: each formats with its own config.
		assert.Equal(t, probeBroken, openAndFormat(t, srv, filepath.Join(dirA, "a.thrift")), "folder A config: width 30 breaks")
		assert.Equal(t, probeOneLine, openAndFormat(t, srv, filepath.Join(dirB, "b.thrift")), "folder B config: width 100 keeps one line")
	})
}

// TestConfigDiscoverySingleFileMode verifies that a session without
// workspace folders discovers the config from the opened file's directory
// at the first didOpen, like the CLI's per-file discovery.
func TestConfigDiscoverySingleFileMode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{"printWidth": 30}`)

		srv := NewServer(cache.NewMemoizedFS(), nil, Options{})
		initWorkspace(t, srv, nil, nil)

		assert.Equal(t, probeBroken, openAndFormat(t, srv, filepath.Join(dir, "app.thrift")))
	})
}

// TestConfigDiscoveryExplicitPathPins verifies that an explicit --config
// file disables per-folder discovery: every view formats with that file,
// whatever the workspace folder contains.
func TestConfigDiscoveryExplicitPathPins(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{"printWidth": 30}`)

		srv := NewServer(cache.NewMemoizedFS(), nil, Options{
			Config:     options.Default(),
			ConfigPath: "/pinned/thrift-ls.json",
		})
		initWorkspace(t, srv, []uri.URI{uri.File(dir)}, nil)

		assert.Equal(t, probeOneLine, openAndFormat(t, srv, filepath.Join(dir, "a.thrift")))
	})
}

// TestConfigDiscoveryDefaultsWhenNoConfig verifies that a folder without a
// config file formats with defaults, not with the startup working-directory
// config (the launcher's, which is meaningless to the workspace).
func TestConfigDiscoveryDefaultsWhenNoConfig(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()

		// A startup CWD-style config must not leak into the view.
		startup := options.Default()
		width := 30
		startup.PrintWidth = &width

		srv := NewServer(cache.NewMemoizedFS(), nil, Options{Config: startup})
		initWorkspace(t, srv, []uri.URI{uri.File(dir)}, nil)

		assert.Equal(t, probeOneLine, openAndFormat(t, srv, filepath.Join(dir, "a.thrift")))
	})
}

// TestConfigDiscoveryWorkspaceSettingsOverlay verifies the layering on a
// discovered config: workspace settings (initializationOptions, then
// didChangeConfiguration) sit on top of the folder's config file.
func TestConfigDiscoveryWorkspaceSettingsOverlay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{"printWidth": 30}`)

		srv := NewServer(cache.NewMemoizedFS(), nil, Options{})
		initWorkspace(t, srv, []uri.URI{uri.File(dir)}, []byte(`{"printWidth": 100}`))

		file := filepath.Join(dir, "a.thrift")

		// The phases share one server: settings evolve sequentially, each
		// on top of the previous state.
		assert.Equal(t, probeOneLine, openAndFormat(t, srv, file), "initializationOptions width 100 wins over the config's 30")

		require.NoError(t, srv.DidChangeConfiguration(t.Context(), &protocol.DidChangeConfigurationParams{
			Settings: protocol.LSPAny([]byte(`{"printWidth": 30}`)),
		}))
		assert.Equal(t, probeBroken, openAndFormat(t, srv, file), "didChangeConfiguration width 30 replaces the overlay")

		require.NoError(t, srv.DidChangeConfiguration(t.Context(), &protocol.DidChangeConfigurationParams{
			Settings: protocol.LSPAny([]byte(`{"printWidth": 30, "align": "bogus"}`)),
		}))
		assert.Equal(t, probeBroken, openAndFormat(t, srv, file), "invalid settings are rejected: the previous overlay stays")
	})
}

// TestConfigDiscoveryLogLevel verifies that the first view's config sets
// the process log level once the workspace is known.
func TestConfigDiscoveryLogLevel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{"logLevel": 5}`)

		srv := NewServer(cache.NewMemoizedFS(), nil, Options{})
		initWorkspace(t, srv, nil, nil)

		openAndFormat(t, srv, filepath.Join(dir, "app.thrift"))

		srv.logLevelMu.Lock()
		defer srv.logLevelMu.Unlock()
		require.NotNil(t, srv.logLevel)
		assert.Equal(t, 5, *srv.logLevel)
	})
}

// TestConfigDiscoveryInvalidFileKeepsDefaults verifies that a malformed
// config file is rejected with the defaults in effect, like invalid
// workspace settings: it must not crash the server.
func TestConfigDiscoveryInvalidFileKeepsDefaults(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{"printWidth": "wide"}`)

		srv := NewServer(cache.NewMemoizedFS(), nil, Options{})
		initWorkspace(t, srv, []uri.URI{uri.File(dir)}, nil)

		assert.Equal(t, probeOneLine, openAndFormat(t, srv, filepath.Join(dir, "a.thrift")))
	})
}

// TestConfigDiscoveryNestedFolder verifies that discovery walks up from
// the workspace folder: a config at the repo root applies to a workspace
// folder nested inside it.
func TestConfigDiscoveryNestedFolder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := t.TempDir()
		writeConfig(t, root, `{"printWidth": 30}`)
		nested := filepath.Join(root, "packages", "app")
		require.NoError(t, os.MkdirAll(nested, 0o755))

		srv := NewServer(cache.NewMemoizedFS(), nil, Options{})
		initWorkspace(t, srv, []uri.URI{uri.File(nested)}, nil)

		assert.Equal(t, probeBroken, openAndFormat(t, srv, filepath.Join(nested, "a.thrift")))
	})
}
