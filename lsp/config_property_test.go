package lsp

// Compares config discovery, layering, and lspSettings handling against
// models derived from the documented precedence rules.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/formatter"
	"github.com/karitham/thrift-ls/internal/proptest"
	"github.com/karitham/thrift-ls/options"
	"github.com/karitham/thrift-ls/store"
)

// patchFields projects a patch onto comparable values, so a failed
// comparison names the field instead of dumping three layers at once.
func patchFields(p options.Patch) map[string]any {
	out := map[string]any{}

	if p.PrintWidth != nil {
		out["printWidth"] = *p.PrintWidth
	}

	if p.TabWidth != nil {
		out["tabWidth"] = *p.TabWidth
	}

	if p.Align != nil {
		out["align"] = *p.Align
	}

	if p.Indent != nil {
		out["indent"] = p.Indent.Value
	}

	if p.IncludePaths != nil {
		out["includePaths"] = append([]string{}, *p.IncludePaths...)
	}

	if p.LogLevel != nil {
		out["logLevel"] = *p.LogLevel
	}

	if p.Separators != nil {
		separators := map[string]string{}
		for _, construct := range formatter.AllConstructs {
			if v := p.Separators.Get(construct); v != nil {
				separators[construct.String()] = *v
			}
		}

		out["separators"] = separators
	}

	if p.Break != nil {
		breaks := map[string]bool{}
		for _, construct := range formatter.AllConstructs {
			if v := p.Break.Get(construct); v != nil {
				breaks[construct.String()] = *v
			}
		}

		out["break"] = breaks
	}

	if p.Lint != nil {
		lint := map[string]any{}

		if p.Lint.Disabled != nil {
			lint["disabled"] = append([]string{}, *p.Lint.Disabled...)
		}

		if p.Lint.Severity != nil {
			severity := map[string]string{}
			maps.Copy(severity, *p.Lint.Severity)

			lint["severity"] = severity
		}

		out["lint"] = lint
	}

	return out
}

// drawPropertyPatch draws one partial options patch with every field
// independently set or unset.
func drawPropertyPatch(t *proptest.T) options.Patch {
	var patch options.Patch

	patch.PrintWidth = proptest.Ptr(t, proptest.IntRange(20, 120), true)
	patch.TabWidth = proptest.Ptr(t, proptest.IntRange(1, 8), true)
	patch.Align = proptest.Ptr(t, proptest.SampledFrom([]string{"field", "assign", "disable"}), true)
	patch.Indent = proptest.Ptr(t, proptest.Map(
		proptest.SampledFrom([]string{"  ", "    ", "\t"}),
		func(value string) formatter.Indent {
			indent, err := formatter.ParseIndentValue(value)
			if err != nil {
				panic(err)
			}

			return indent
		},
	), true)

	if proptest.Bool(t) {
		var separators formatter.Separators

		for _, construct := range formatter.AllConstructs {
			if !proptest.Bool(t) {
				continue
			}

			separators.Set(construct, new(proptest.Pick(t, []string{"comma", "semicolon", "none", "preserve"})))
		}

		patch.Separators = &separators
	}

	if proptest.Bool(t) {
		var breaks formatter.Break

		for _, construct := range formatter.AllConstructs {
			if proptest.Bool(t) {
				breaks.Set(construct, new(proptest.Bool(t)))
			}
		}

		patch.Break = &breaks
	}

	if proptest.Bool(t) {
		patch.IncludePaths = proptest.Ptr(t, proptest.SliceOfN(
			proptest.SampledFrom([]string{"inc", "../shared", "gen", "/abs/inc"}), 0, 3,
		), false)
	}

	patch.LogLevel = proptest.Ptr(t, proptest.IntRange(-4, 8), true)

	if proptest.Bool(t) {
		lint := options.LintConfig{}
		lint.Disabled = proptest.Ptr(t, proptest.SliceOfN(
			proptest.SampledFrom([]string{"duplicate", "field-id", "unused-include"}), 0, 2,
		), true)
		lint.Severity = proptest.Ptr(t, proptest.MapOfN(
			proptest.SampledFrom([]string{"duplicate", "field-id"}), proptest.SampledFrom([]string{"error", "warning", "info", "hint"}), 0, 2,
		), true)
		patch.Lint = &lint
	}

	return patch
}

// patchDocument renders the set fields of a patch as a thrift-ls.json body.
func patchDocument(patch options.Patch) map[string]any {
	doc := map[string]any{}

	if patch.PrintWidth != nil {
		doc["printWidth"] = *patch.PrintWidth
	}

	if patch.TabWidth != nil {
		doc["tabWidth"] = *patch.TabWidth
	}

	if patch.Align != nil {
		doc["align"] = *patch.Align
	}

	if patch.Indent != nil {
		doc["indent"] = patch.Indent.Value
	}

	if patch.IncludePaths != nil {
		doc["includePaths"] = append([]string{}, *patch.IncludePaths...)
	}

	if patch.LogLevel != nil {
		doc["logLevel"] = *patch.LogLevel
	}

	if patch.Separators != nil {
		separators := map[string]any{}
		for _, construct := range formatter.AllConstructs {
			if v := patch.Separators.Get(construct); v != nil {
				separators[construct.String()] = *v
			}
		}

		doc["separators"] = separators
	}

	if patch.Break != nil {
		breaks := map[string]any{}
		for _, construct := range formatter.AllConstructs {
			if v := patch.Break.Get(construct); v != nil {
				breaks[construct.String()] = *v
			}
		}

		doc["break"] = breaks
	}

	if patch.Lint != nil {
		lint := map[string]any{}

		if patch.Lint.Disabled != nil {
			lint["disabled"] = append([]string{}, *patch.Lint.Disabled...)
		}

		if patch.Lint.Severity != nil {
			severity := map[string]any{}
			for code, level := range *patch.Lint.Severity {
				severity[code] = level
			}

			lint["severity"] = severity
		}

		doc["lint"] = lint
	}

	return doc
}

func mustJSON(t require.TestingT, value any) []byte {
	data, err := json.Marshal(value)
	require.NoError(t, err)

	return data
}

// initPropertyWorkspace initializes a server over folders and loads them
// synchronously through the default loader.
func initPropertyWorkspace(tb require.TestingT, srv *Server, folders []uri.URI) {
	_, err := srv.Initialize(context.Background(), testInitializeParams(foldersFromURIs(folders)))
	require.NoError(tb, err)

	srv.workspace.loadSync(context.Background(), folders)
}

// FuzzFileConfigSourceDiscovery checks nearest-config lookup, include-path
// anchoring, the THRIFT_LS_CONFIG override, and malformed-document errors.
func FuzzFileConfigSourceDiscovery(f *testing.F) {
	f.Setenv("THRIFT_LS_CONFIG", "")

	segments := []string{"alpha", "beta", "gamma"}

	proptest.Fuzz(f, proptest.Corpus{
		[]byte(""),
		[]byte("root only"),
		[]byte("nested config"),
		[]byte("four depths"),
		[]byte("malformed config"),
		[]byte("override"),
	}, func(t *proptest.T) {
		depth := proptest.Int(t, 0, 3)

		path := "/cfg-root"
		for i := range depth {
			path = filepath.Join(path, segments[i])
		}

		// Choose which ancestors hold a document. Each carries a different
		// print width, so the winner is identifiable from the value alone.
		ancestors := []string{}
		for dir := path; ; dir = filepath.Dir(dir) {
			ancestors = append(ancestors, dir)
			if dir == "/" || dir == filepath.Dir(dir) {
				break
			}
		}

		docDirs := map[string]int{}
		for _, dir := range ancestors {
			if !proptest.Bool(t) {
				continue
			}

			docDirs[dir] = proptest.Int(t, 20, 60)
		}

		seeded := map[uri.URI][]byte{}
		malformed := map[string]int{}
		for dir, width := range docDirs {
			kind := proptest.Int(t, 0, 5)
			malformed[dir] = kind

			switch kind {
			case 0:
				seeded[uri.File(filepath.Join(dir, options.ConfigFileName))] = fmt.Appendf(nil, `{"printWidth": %d, "includePaths": ["inc", "../shared"]}`, width)
			case 1:
				seeded[uri.File(filepath.Join(dir, options.ConfigFileName))] = []byte(`{"printWidth": }`)
			default:
				seeded[uri.File(filepath.Join(dir, options.ConfigFileName))] = []byte(`{"printwidthh": 100}`)
			}
		}

		// THRIFT_LS_CONFIG, when set, replaces discovery: it may name a
		// seeded document, a malformed one, or nothing at all.
		override := proptest.Int(t, 0, 4)
		overridePath := "/env/explicit.json"

		switch override {
		case 1:
			seeded[uri.File(overridePath)] = []byte(`{"printWidth": 111}`)
		case 2:
			seeded[uri.File(overridePath)] = []byte(`{"tabWidth": 0}`)
		}

		if override > 0 {
			t.Setenv("THRIFT_LS_CONFIG", overridePath)
		} else {
			t.Setenv("THRIFT_LS_CONFIG", "")
		}

		source := FileConfigSource(store.NewMemFS(seeded))
		resolved, err := source(path)

		// Model.
		wantPath := ""
		wantWidth := 0
		wantErr := false
		wantMissing := false

		if override > 0 {
			wantPath = overridePath

			switch override {
			case 1:
				wantWidth = 111
			case 2:
				wantErr = true
			default:
				wantErr = true
				wantMissing = true
			}
		} else {
			for _, dir := range ancestors { // nearest first
				if width, ok := docDirs[dir]; ok {
					wantPath = filepath.Join(dir, options.ConfigFileName)
					wantWidth = width
					wantErr = malformed[dir] != 0

					break
				}
			}
		}

		require.Equal(t, wantPath, resolved.Path, "resolved config path")

		if wantErr {
			require.Error(t, err, "a rejected or missing document must surface as an error")

			if wantMissing {
				require.True(t, errors.Is(err, fs.ErrNotExist), "a missing THRIFT_LS_CONFIG target is fs.ErrNotExist")
			}

			return
		}

		require.NoError(t, err)

		if wantPath == "" {
			require.Nil(t, resolved.Patch, "no document anywhere means an empty resolution")

			return
		}

		require.NotNil(t, resolved.Patch)

		if wantWidth != 0 {
			require.NotNil(t, resolved.Patch.PrintWidth)
			require.Equal(t, wantWidth, *resolved.Patch.PrintWidth, "nearest document wins")
		}

		if resolved.Patch.IncludePaths != nil {
			docDir := filepath.Dir(wantPath)
			require.Equal(t, []string{filepath.Join(docDir, "inc"), filepath.Join(docDir, "../shared")}, *resolved.Patch.IncludePaths,
				"relative include paths anchor to the document's directory")
		}
	})
}

// FuzzProjectViewConfigLayering is the layering model at the server seam:
// defaults, then Frontend.Defaults, then the discovered document, then CLI,
// and a loader's non-empty include paths override both later layers.
func FuzzProjectViewConfigLayering(f *testing.F) {
	f.Setenv("THRIFT_LS_CONFIG", "")

	proptest.Fuzz(f, proptest.Corpus{
		[]byte(""),
		[]byte("defaults only"),
		[]byte("document at root"),
		[]byte("document above root"),
		[]byte("malformed document"),
		[]byte("loader include paths"),
	}, func(t *proptest.T) {
		rootPath := proptest.Pick(t, []string{"/proj", "/proj/sub", "/proj/sub/deeper"})

		frontend := drawPropertyPatch(t)
		document := drawPropertyPatch(t)
		cli := drawPropertyPatch(t)

		// The document may sit at the root or one level up, and may be
		// malformed: then it is skipped and an issue diagnostic appears.
		docDepth := proptest.Int(t, 0, 2)
		docPath := rootPath
		for range docDepth {
			docPath = filepath.Dir(docPath)
		}

		docURI := uri.File(filepath.Join(docPath, options.ConfigFileName))
		broken := proptest.Int(t, 0, 5) == 0

		files := map[uri.URI][]byte{}
		if broken {
			files[docURI] = []byte(`{"align": "diagonal"}`)
		} else {
			files[docURI] = mustJSON(t, patchDocument(document))
		}

		client := &workspaceRecordingClient{}
		srv := newSyncServerWithOptions(client, files, Options{Defaults: frontend, CLI: cli})
		initPropertyWorkspace(t, srv, []uri.URI{uri.File(rootPath)})

		layers := []options.Patch{frontend}
		if !broken {
			// A discovered document anchors its relative include paths to
			// its own directory; absolute ones pass through.
			anchored := document
			if document.IncludePaths != nil {
				paths := make([]string, 0, len(*document.IncludePaths))
				for _, include := range *document.IncludePaths {
					if filepath.IsAbs(include) {
						paths = append(paths, include)

						continue
					}

					paths = append(paths, filepath.Join(docPath, include))
				}

				anchored.IncludePaths = &paths
			}

			layers = append(layers, anchored)
		}

		layers = append(layers, cli)

		want := proptest.ModelLayered(options.Default(), layers...)
		got := srv.folderConfig(uri.File(rootPath))

		require.Equal(t, patchFields(want), patchFields(got), "effective options for %s", rootPath)

		if broken {
			msgs := diagMessages(client.last(docURI))
			require.Len(t, msgs, 1, "a rejected document is reported at its own path")
			require.Contains(t, msgs[0], "invalid configuration")
		}

		// A loader's include paths are authoritative over the document and
		// CLI, and empty ones fall back to them.
		loaderIncludes := proptest.Pick(t, [][]string{nil, {"/from-build-system"}, {"/one", "/two"}})

		project := Project{
			ConfigURI:    uri.File(filepath.Join(rootPath, "build.json")),
			RootURI:      uri.File(rootPath),
			IncludePaths: loaderIncludes,
		}

		custom := newSyncServerWithOptions(client, files, Options{Defaults: frontend, CLI: cli})
		custom.workspace = newWorkspace(custom, nil)
		_, err := custom.Initialize(context.Background(), testInitializeParams(foldersFromURIs([]uri.URI{uri.File(rootPath)})))
		require.NoError(t, err)
		installPropertySnapshots(t, custom, map[uri.URI]WorkspaceSnapshot{
			uri.File(rootPath): {Projects: []Project{project}},
		})

		got = custom.folderConfig(uri.File(rootPath))
		require.Equal(t, patchFields(stripIncludePaths(want)), patchFields(stripIncludePaths(got)),
			"non-include fields follow the layering order")

		if len(loaderIncludes) > 0 {
			require.NotNil(t, got.IncludePaths)
			require.Equal(t, loaderIncludes, *got.IncludePaths, "loader include paths win over document and CLI")
		} else {
			wantPaths, wantSet := includePathsOf(want)
			gotPaths, gotSet := includePathsOf(got)

			require.Equal(t, wantSet, gotSet, "empty loader include paths fall back to the document and CLI layers")
			require.Equal(t, wantPaths, gotPaths, "empty loader include paths fall back to the document and CLI layers")
		}
	})
}

// includePathsOf reports the include paths and whether they were set at all.
func includePathsOf(p options.Patch) ([]string, bool) {
	if p.IncludePaths == nil {
		return nil, false
	}

	return *p.IncludePaths, true
}

// stripIncludePaths returns p without its include paths, for comparing the
// layers that the loader has no opinion about.
func stripIncludePaths(p options.Patch) options.Patch {
	p.IncludePaths = nil

	return p
}

// FuzzLSPSettingsContract checks that lspSettings drops the path extension,
// rejects unknown keys, and keeps the previous settings after a rejection.
func FuzzLSPSettingsContract(f *testing.F) {
	proptest.Fuzz(f, proptest.Corpus{
		[]byte(""),
		[]byte("path only"),
		[]byte("every field"),
		[]byte("unknown key"),
		[]byte("severity map"),
	}, func(t *proptest.T) {
		patch := drawPropertyPatch(t)

		doc := patchDocument(patch)
		doc["path"] = proptest.Pick(t, []string{"/usr/bin/thrift-ls", "", "/tmp/bin"})

		got, err := lspSettings(mustJSON(t, doc))
		require.NoError(t, err)
		require.Equal(t, patchFields(patch), patchFields(*got), "settings round-trip, path dropped")

		// Unknown keys are rejected, never silently ignored: a typo must not
		// format with stale options.
		unknown := proptest.Pick(t, []string{"printWhidt", "includePath", "lintt", "algin", "tabwidht"})
		bad := patchDocument(patch)
		bad["path"] = "/bin/x"
		bad[unknown] = proptest.Pick(t, []any{100, "field", true, []any{"x"}})

		_, err = lspSettings(mustJSON(t, bad))
		require.Error(t, err, "unknown key %q must be rejected", unknown)
		require.Contains(t, err.Error(), unknown)

		// A rejected update keeps the previous settings exactly.
		srv := newSyncServerWithOptions(nil, nil, Options{})
		srv.setWorkspaceSettings(*got)

		srv.optsMu.RLock()
		before := srv.workspaceOverlay
		srv.optsMu.RUnlock()

		broken := *got
		broken.PrintWidth = new(0)
		srv.setWorkspaceSettings(broken)

		srv.optsMu.RLock()
		after := srv.workspaceOverlay
		srv.optsMu.RUnlock()

		require.Equal(t, patchFields(before), patchFields(after), "a rejected settings update must not change the overlay")
	})
}
