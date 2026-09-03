package lsp

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/formatter"
	"github.com/karitham/thrift-ls/options"
	"github.com/karitham/thrift-ls/resolver/resolvertest"
)

// Probe: printWidth 80 keeps the long struct on one line, 30 breaks it.
const probe = "struct LongName{1: string fieldNameThatIsQuiteLong}\n"

const (
	probeOneLine = "struct LongName { 1: string fieldNameThatIsQuiteLong }\n"
	probeBroken  = "struct LongName {\n    1: string fieldNameThatIsQuiteLong\n}\n"
)

// TestConfigDiscoveryPerWorkspaceFolder verifies that each workspace
// folder formats with its own thrift-ls.json: no single process-global
// config baked in before the workspace was known.
func TestConfigDiscoveryPerWorkspaceFolder(t *testing.T) {
	t.Setenv("THRIFT_LS_CONFIG", "")

	dirA, dirB := "/ws/a", "/ws/b"
	files := seedFiles(map[string]string{
		"/ws/a/thrift-ls.json": `{"printWidth": 30}`,
		"/ws/b/thrift-ls.json": `{"printWidth": 100}`,
	})

	srv := newSyncServerWithOptions(nil, files, Options{})
	initWorkspace(t, srv, []uri.URI{uri.File(dirA), uri.File(dirB)}, nil)

	assert.Equal(t, probeBroken, openAndFormat(t, srv, "/ws/a/a.thrift"), "folder A config: width 30 breaks")
	assert.Equal(t, probeOneLine, openAndFormat(t, srv, "/ws/b/b.thrift"), "folder B config: width 100 keeps one line")
}

// TestConfigDiscovery folds the single-folder discovery cases into one
// table: single-file mode, defaults without config or CWD leak, pinned
// source bypass, invalid file keeps defaults, and nested walk-up. Each
// case opens one file and asserts the formatted probe.
func TestConfigDiscovery(t *testing.T) {
	for _, tt := range []struct {
		name string
		// config is written to the target dir; empty means no config file.
		config string
		// pinned disables per-folder discovery entirely.
		pinned bool
		// nested opens a workspace folder below the config dir.
		nested bool
		// singleFile initializes with no folders (per-file discovery).
		singleFile bool
		want       string
	}{
		{name: "single file mode discovers from the file dir", config: `{"printWidth": 30}`, singleFile: true, want: probeBroken},
		{name: "pinned source ignores the folder config", config: `{"printWidth": 30}`, pinned: true, want: probeOneLine},
		{name: "no config formats with defaults, no CWD leak", want: probeOneLine},
		{name: "invalid file keeps defaults", config: `{"printWidth": "wide"}`, want: probeOneLine},
		{name: "nested folder walks up to the repo root", config: `{"printWidth": 30}`, nested: true, want: probeBroken},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("THRIFT_LS_CONFIG", "")

			root := "/ws/root"
			folder := root
			if tt.nested {
				folder = "/ws/root/packages/app"
			}

			entries := map[string]string{}
			if tt.config != "" {
				entries["/ws/root/thrift-ls.json"] = tt.config
			}
			files := seedFiles(entries)

			var srv *Server
			if tt.pinned {
				srv = newSyncServerWithOptions(nil, files, Options{ConfigSource: options.PinnedSource(nil)})
			} else {
				srv = newSyncServerWithOptions(nil, files, Options{})
			}

			var folders []uri.URI
			if !tt.singleFile {
				folders = []uri.URI{uri.File(folder)}
			}
			initWorkspace(t, srv, folders, nil)

			assert.Equal(t, tt.want, openAndFormat(t, srv, folder+"/a.thrift"))
		})
	}
}

// TestConfigDiscoveryWorkspaceSettingsOverlay verifies the layering on a
// discovered config: workspace settings (initializationOptions, then
// didChangeConfiguration) sit on top of the folder's config file.
func TestConfigDiscoveryWorkspaceSettingsOverlay(t *testing.T) {
	t.Setenv("THRIFT_LS_CONFIG", "")

	dir := "/ws/proj"
	files := seedFiles(map[string]string{"/ws/proj/thrift-ls.json": `{"printWidth": 30}`})

	srv := newSyncServerWithOptions(nil, files, Options{})
	initWorkspace(t, srv, []uri.URI{uri.File(dir)}, []byte(`{"printWidth": 100}`))

	file := "/ws/proj/a.thrift"

	assert.Equal(t, probeOneLine, openAndFormat(t, srv, file), "initializationOptions width 100 wins over the config's 30")

	require.NoError(t, srv.DidChangeConfiguration(t.Context(), &protocol.DidChangeConfigurationParams{
		Settings: protocol.LSPAny([]byte(`{"printWidth": 30}`)),
	}))
	assert.Equal(t, probeBroken, openAndFormat(t, srv, file), "didChangeConfiguration width 30 replaces the overlay")

	require.NoError(t, srv.DidChangeConfiguration(t.Context(), &protocol.DidChangeConfigurationParams{
		Settings: protocol.LSPAny([]byte(`{"printWidth": 30, "align": "bogus"}`)),
	}))
	assert.Equal(t, probeBroken, openAndFormat(t, srv, file), "invalid settings are rejected: the previous overlay stays")
}

// TestConfigDiscoveryLogLevel verifies that the first view's config sets
// the process log level once the workspace is known.
func TestConfigDiscoveryLogLevel(t *testing.T) {
	t.Setenv("THRIFT_LS_CONFIG", "")

	dir := "/ws/proj"
	files := seedFiles(map[string]string{"/ws/proj/thrift-ls.json": `{"logLevel": 5}`})

	srv := newSyncServerWithOptions(nil, files, Options{})
	initWorkspace(t, srv, nil, nil)

	openAndFormat(t, srv, dir+"/app.thrift")

	srv.logLevelMu.Lock()
	defer srv.logLevelMu.Unlock()
	require.NotNil(t, srv.logLevel)
	assert.Equal(t, 5, *srv.logLevel)
}

func TestConfigDiscoveryAppliesConfiguredDefaults(t *testing.T) {
	defaults := options.Patch{FormatPatch: formatter.FormatPatch{
		Indent: &formatter.Indent{Value: "  ", Width: 2},
		Break:  &formatter.Break{Structs: new(true)},
	}}

	for _, tt := range []struct {
		name   string
		config string
		want   string
	}{
		{
			name:   "discovered width retains the configured indentation",
			config: `{"printWidth": 30}`,
			want:   "struct LongName {\n  1: string fieldNameThatIsQuiteLong\n}\n",
		},
		{
			name:   "discovered break setting overrides the configured default",
			config: `{"break":{"structs":false}}`,
			want:   probeOneLine,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("THRIFT_LS_CONFIG", "")

			files := seedFiles(map[string]string{"/ws/root/thrift-ls.json": tt.config})
			srv := newSyncServerWithOptions(nil, files, Options{Defaults: defaults})
			initWorkspace(t, srv, []uri.URI{uri.File("/ws/root")}, nil)

			assert.Equal(t, tt.want, openAndFormat(t, srv, "/ws/root/a.thrift"))
		})
	}
}

// TestProjectConfigLayering pins the loader-project precedence: the loader
// carries include paths only — formatting comes from the file document and
// CLI. Project include paths stay authoritative over both.
func TestProjectConfigLayering(t *testing.T) {
	for _, tt := range []struct {
		name      string
		file      int
		cli       int
		wantWidth int
		wantText  string
	}{
		{name: "file sets format, project sets includes", file: 30, wantWidth: 30, wantText: probeBroken},
		{name: "cli beats file", file: 30, cli: 100, wantWidth: 100, wantText: probeOneLine},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := uri.File("/workspace/proj")
			target := uri.File("/workspace/proj/api.thrift")
			fileWidth := tt.file
			fileDoc := &options.Patch{FormatPatch: formatter.FormatPatch{PrintWidth: &fileWidth}}
			var cliPatch options.Patch
			if tt.cli > 0 {
				cliWidth := tt.cli
				cliPatch = options.Patch{FormatPatch: formatter.FormatPatch{PrintWidth: &cliWidth}}
			}
			projectIncludes := []string{"/build/includes"}
			fileDoc.IncludePaths = &[]string{"/file/includes"}
			cliPatch.IncludePaths = &[]string{"/cli/includes"}

			srv := newSyncServerWithOptions(nil,
				seedFiles(map[string]string{"/workspace/proj/api.thrift": "struct API {}"}),
				Options{
					ConfigSource: options.PinnedSource(fileDoc),
					CLI:          cliPatch,
				})
			initCustomFolders(t, srv, []uri.URI{uri.File("/workspace")})
			installSnapshot(t, srv, uri.File("/workspace"), WorkspaceSnapshot{Projects: []Project{{
				ConfigURI:    uri.File("/workspace/proj.json"),
				RootURI:      root,
				TargetFiles:  []uri.URI{target},
				IncludePaths: projectIncludes,
			}}})

			cfg := srv.folderConfig(root)
			require.NotNil(t, cfg.PrintWidth)
			assert.Equal(t, tt.wantWidth, *cfg.PrintWidth)
			require.NotNil(t, cfg.IncludePaths)
			assert.Equal(t, projectIncludes, *cfg.IncludePaths)
			assert.Equal(t, tt.wantText, openAndFormat(t, srv, target.FsPath()))
		})
	}
}

// TestWorkspaceLoaderUsesConfigSourcePerProjectRoot verifies the
// ConfigSource is consulted once per project root, and each view formats
// with its own document.
func TestWorkspaceLoaderUsesConfigSourcePerProjectRoot(t *testing.T) {
	roots := []string{"/ws/one", "/ws/two"}
	widths := []int{91, 92}
	patches := make(map[string]*options.Patch, len(roots))
	projects := make([]Project, len(roots))
	entries := map[string]string{}
	for i, root := range roots {
		width := widths[i]
		patches[root] = &options.Patch{FormatPatch: formatter.FormatPatch{PrintWidth: &width}}
		entries[root+"/api.thrift"] = "struct API {}"
		projects[i] = Project{
			ConfigURI:   uri.File(root + "/project.json"),
			RootURI:     uri.File(root),
			TargetFiles: []uri.URI{uri.File(root + "/api.thrift")},
		}
	}

	var calls []string
	source := func(root string) (options.Resolved, error) {
		calls = append(calls, root)

		return options.Resolved{Patch: patches[root]}, nil
	}
	srv := newSyncServerWithOptions(nil, seedFiles(entries), Options{ConfigSource: source})
	initCustomFolders(t, srv, []uri.URI{uri.File("/ws")})
	installSnapshot(t, srv, uri.File("/ws"), WorkspaceSnapshot{Projects: projects})

	slices.Sort(calls)
	assert.Equal(t, roots, calls)
	for i, root := range roots {
		cfg := srv.folderConfig(uri.File(root))
		require.NotNil(t, cfg.PrintWidth)
		assert.Equal(t, widths[i], *cfg.PrintWidth)
	}
}

// TestPinnedSourceBypassesDiscovery verifies a pinned document applies to
// loader projects without consulting disk.
func TestPinnedSourceBypassesDiscovery(t *testing.T) {
	root := uri.File("/workspace/project")
	target := uri.File("/workspace/project/api.thrift")
	width := 97
	pinned := options.Patch{FormatPatch: formatter.FormatPatch{PrintWidth: &width}}

	srv := newSyncServerWithOptions(nil,
		seedFiles(map[string]string{"/workspace/project/api.thrift": "struct API {}"}),
		Options{ConfigSource: options.PinnedSource(&pinned)})
	initCustomFolders(t, srv, []uri.URI{uri.File("/workspace")})
	installSnapshot(t, srv, uri.File("/workspace"), WorkspaceSnapshot{Projects: []Project{{
		ConfigURI:   uri.File("/workspace/project/project.json"),
		RootURI:     root,
		TargetFiles: []uri.URI{target},
	}}})

	cfg := srv.folderConfig(root)
	require.NotNil(t, cfg.PrintWidth)
	assert.Equal(t, width, *cfg.PrintWidth)
}

// TestConfigFileIncludePaths verifies that include paths from a workspace
// folder's thrift-ls.json flow through view creation into the snapshot's
// resolver, resolved relative to the config file.
func TestConfigFileIncludePaths(t *testing.T) {
	t.Setenv("THRIFT_LS_CONFIG", "")

	files := resolvertest.Map{
		"/ws/proj/thrift-ls.json":     []byte(`{"includePaths": ["base"]}`),
		"/ws/proj/base/shared.thrift": []byte("struct Shared {}"),
	}.URIs()

	srv := newSyncServerWithOptions(nil, files, Options{})
	initWorkspace(t, srv, []uri.URI{uri.File("/ws/proj")}, nil)

	app := uri.File("/ws/proj/app.thrift")
	view, err := srv.session.ViewOf(app)
	require.NoError(t, err)

	assert.Equal(t, []string{"/ws/proj/base"}, view.Resolver().IncludePaths())

	resolved := view.Resolver().ResolveInclude(t.Context(), app, "shared.thrift")
	assert.Equal(t, uri.File("/ws/proj/base/shared.thrift"), resolved)
}

// TestCustomProjectIncludePathsAreAuthoritative verifies a loader
// project's include paths win over the file document, CLI, and workspace
// settings.
func TestCustomProjectIncludePathsAreAuthoritative(t *testing.T) {
	root := uri.File("/ws/project")
	target := uri.File("/ws/project/api.thrift")
	projectIncludes := "/ws/project-includes"
	configIncludes := "/ws/config-includes"
	cliIncludes := "/ws/cli-includes"
	settingsIncludes := "/ws/settings-includes"

	fileDoc := &options.Patch{IncludePaths: &[]string{configIncludes}}
	files := seedFiles(map[string]string{
		"/ws/project/api.thrift":             `include "shared.thrift"`,
		"/ws/project-includes/shared.thrift": "struct Shared {}",
	})
	srv := newSyncServerWithOptions(nil, files, Options{
		CLI:          options.Patch{IncludePaths: &[]string{cliIncludes}},
		ConfigSource: options.PinnedSource(fileDoc),
	})
	params := testInitializeParams([]protocol.WorkspaceFolder{{URI: uri.File("/ws")}})
	params.InitializationOptions = protocol.LSPAny([]byte(`{"includePaths":["` + settingsIncludes + `"]}`))
	_, err := srv.Initialize(t.Context(), params)
	require.NoError(t, err)

	installSnapshot(t, srv, uri.File("/ws"), WorkspaceSnapshot{Projects: []Project{{
		ConfigURI:    uri.File("/ws/project/project.json"),
		RootURI:      root,
		TargetFiles:  []uri.URI{target},
		IncludePaths: []string{projectIncludes},
	}}})

	view, err := srv.session.ViewOf(target)
	require.NoError(t, err)
	assert.Equal(t, []string{projectIncludes}, view.Resolver().IncludePaths())
	assert.Equal(t, uri.File("/ws/project-includes/shared.thrift"), view.Resolver().ResolveInclude(t.Context(), target, "shared.thrift"))

	_ = context.Background
}

func TestLSPSettings(t *testing.T) {
	t.Run("parses options and drops the path key", func(t *testing.T) {
		patch, err := lspSettings([]byte(`{"path":"/usr/bin/thrift-ls","printWidth":30,"align":"assign"}`))
		require.NoError(t, err)
		require.NotNil(t, patch.PrintWidth)
		assert.Equal(t, 30, *patch.PrintWidth)
		assert.Equal(t, "assign", *patch.Align)
	})

	t.Run("rejects unknown keys", func(t *testing.T) {
		_, err := lspSettings([]byte(`{"printWidth":30,"typoKey":1}`))
		assert.Error(t, err)
	})

	t.Run("rejects invalid values", func(t *testing.T) {
		_, err := lspSettings([]byte(`{"align":"bogus"}`))
		assert.Error(t, err)
	})
}

func TestWorkspaceSettings(t *testing.T) {
	const file = "file:///tmp/settings.thrift"
	content := "struct LongName{1: string fieldNameThatIsQuiteLong}\n"

	ctx := t.Context()
	srv := newMemServer(nil)

	require.NoError(t, srv.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        file,
			LanguageID: "thrift",
			Version:    0,
			Text:       content,
		},
	}))

	assert.Equal(t, probeOneLine, formatText(t, srv, file))

	_, err := srv.Initialize(ctx, &protocol.InitializeParams{
		InitializationOptions: protocol.LSPAny([]byte(`{"printWidth":30}`)),
	})
	require.NoError(t, err)
	assert.Equal(t, probeBroken, formatText(t, srv, file))

	require.NoError(t, srv.DidChangeConfiguration(ctx, &protocol.DidChangeConfigurationParams{
		Settings: protocol.LSPAny([]byte(`{"printWidth":80}`)),
	}))
	assert.Equal(t, probeOneLine, formatText(t, srv, file))

	require.NoError(t, srv.DidChangeConfiguration(ctx, &protocol.DidChangeConfigurationParams{
		Settings: protocol.LSPAny([]byte(`{"printWidth":30,"align":"bogus"}`)),
	}))
	assert.Equal(t, probeOneLine, formatText(t, srv, file))
}
