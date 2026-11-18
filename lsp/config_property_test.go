package lsp

// Compares config discovery, layering, and lspSettings handling against
// models derived from the documented precedence rules.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.lsp.dev/uri"
	"pgregory.net/rapid"

	"github.com/karitham/thrift-ls/formatter"
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
			for code, level := range *p.Lint.Severity {
				severity[code] = level
			}

			lint["severity"] = severity
		}

		out["lint"] = lint
	}

	return out
}

// drawPropertyPatch draws one partial options patch with every field
// independently set or unset.
func drawPropertyPatch(t *rapid.T, name string) options.Patch {
	var patch options.Patch

	patch.PrintWidth = rapid.Ptr(rapid.IntRange(20, 120), true).Draw(t, name+".printWidth")
	patch.TabWidth = rapid.Ptr(rapid.IntRange(1, 8), true).Draw(t, name+".tabWidth")
	patch.Align = rapid.Ptr(rapid.SampledFrom([]string{"field", "assign", "disable"}), true).Draw(t, name+".align")
	patch.Indent = rapid.Ptr(rapid.Map(
		rapid.SampledFrom([]string{"  ", "    ", "\t"}),
		func(value string) formatter.Indent {
			indent, err := formatter.ParseIndentValue(value)
			if err != nil {
				panic(err)
			}

			return indent
		},
	), true).Draw(t, name+".indent")

	if rapid.Bool().Draw(t, name+".separatorsSet") {
		var separators formatter.Separators

		for _, construct := range formatter.AllConstructs {
			if !rapid.Bool().Draw(t, name+".separator."+construct.String()) {
				continue
			}

			separators.Set(construct, new(rapid.SampledFrom([]string{"comma", "semicolon", "none", "preserve"}).Draw(t, name+".separatorValue")))
		}

		patch.Separators = &separators
	}

	if rapid.Bool().Draw(t, name+".breakSet") {
		var breaks formatter.Break

		for _, construct := range formatter.AllConstructs {
			if rapid.Bool().Draw(t, name+".break."+construct.String()) {
				breaks.Set(construct, new(rapid.Bool().Draw(t, name+".breakValue")))
			}
		}

		patch.Break = &breaks
	}

	if rapid.Bool().Draw(t, name+".includePathsSet") {
		patch.IncludePaths = rapid.Ptr(rapid.SliceOfN(
			rapid.SampledFrom([]string{"inc", "../shared", "gen", "/abs/inc"}), 0, 3,
		), false).Draw(t, name+".includePaths")
	}

	patch.LogLevel = rapid.Ptr(rapid.IntRange(-4, 8), true).Draw(t, name+".logLevel")

	if rapid.Bool().Draw(t, name+".lintSet") {
		lint := options.LintConfig{}
		lint.Disabled = rapid.Ptr(rapid.SliceOfN(
			rapid.SampledFrom([]string{"duplicate", "field-id", "unused-include"}), 0, 2,
		), true).Draw(t, name+".lint.disabled")
		lint.Severity = rapid.Ptr(rapid.MapOfN(
			rapid.SampledFrom([]string{"duplicate", "field-id"}), rapid.SampledFrom([]string{"error", "warning", "info", "hint"}), 0, 2,
		), true).Draw(t, name+".lint.severity")
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

// modelLayered applies the documented precedence: every field takes its
// value from the highest layer that sets it, and per-construct maps resolve
// construct by construct.
func modelLayered(lowest options.Patch, layers ...options.Patch) options.Patch {
	all := append([]options.Patch{lowest}, layers...) // ascending precedence

	var out options.Patch

	for i := len(all) - 1; i >= 0; i-- {
		patch := all[i]

		if out.PrintWidth == nil {
			out.PrintWidth = patch.PrintWidth
		}

		if out.TabWidth == nil {
			out.TabWidth = patch.TabWidth
		}

		if out.Align == nil {
			out.Align = patch.Align
		}

		if out.Indent == nil {
			out.Indent = patch.Indent
		}

		if out.IncludePaths == nil {
			out.IncludePaths = patch.IncludePaths
		}

		if out.LogLevel == nil {
			out.LogLevel = patch.LogLevel
		}

		if out.Lint == nil {
			out.Lint = patch.Lint
		}
	}

	out.Separators = modelPerConstruct(all, func(p options.Patch) *formatter.Separators { return p.Separators })
	out.Break = modelPerConstruct(all, func(p options.Patch) *formatter.Break { return p.Break })

	return out
}

func modelPerConstruct[T comparable](all []options.Patch, get func(options.Patch) *formatter.PerConstruct[T]) *formatter.PerConstruct[T] {
	var out *formatter.PerConstruct[T]

	for i := len(all) - 1; i >= 0; i-- {
		src := get(all[i])
		if src == nil {
			continue
		}

		if out == nil {
			out = &formatter.PerConstruct[T]{}
		}

		for _, construct := range formatter.AllConstructs {
			var unset T
			if out.Get(construct) != unset {
				continue
			}

			if v := src.Get(construct); v != unset {
				out.Set(construct, v)
			}
		}
	}

	return out
}

// initPropertyWorkspace initializes a server over folders and loads them
// synchronously through the default loader.
func initPropertyWorkspace(tb require.TestingT, srv *Server, folders []uri.URI) {
	_, err := srv.Initialize(context.Background(), testInitializeParams(foldersFromURIs(folders)))
	require.NoError(tb, err)

	srv.workspace.loadSync(context.Background(), folders)
}

// TestFileConfigSourceDiscovery checks nearest-config lookup, include-path
// anchoring, the THRIFT_LS_CONFIG override, and malformed-document errors.
func TestFileConfigSourceDiscovery(t *testing.T) {
	t.Setenv("THRIFT_LS_CONFIG", "")

	segments := []string{"alpha", "beta", "gamma"}

	outer := t
	rapid.Check(t, func(t *rapid.T) {
		depth := rapid.IntRange(0, 3).Draw(t, "depth")

		path := "/cfg-root"
		for i := 0; i < depth; i++ {
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
		for i, dir := range ancestors {
			if !rapid.Bool().Draw(t, fmt.Sprintf("doc%d", i)) {
				continue
			}

			docDirs[dir] = rapid.IntRange(20, 60).Draw(t, fmt.Sprintf("width%d", i))
		}

		seeded := map[uri.URI][]byte{}
		malformed := map[string]int{}
		for dir, width := range docDirs {
			kind := rapid.IntRange(0, 5).Draw(t, "malformed")
			malformed[dir] = kind

			switch kind {
			case 0:
				seeded[uri.File(filepath.Join(dir, options.ConfigFileName))] = []byte(fmt.Sprintf(`{"printWidth": %d, "includePaths": ["inc", "../shared"]}`, width))
			case 1:
				seeded[uri.File(filepath.Join(dir, options.ConfigFileName))] = []byte(`{"printWidth": }`)
			default:
				seeded[uri.File(filepath.Join(dir, options.ConfigFileName))] = []byte(`{"printwidthh": 100}`)
			}
		}

		// THRIFT_LS_CONFIG, when set, replaces discovery: it may name a
		// seeded document, a malformed one, or nothing at all.
		override := rapid.IntRange(0, 4).Draw(t, "override")
		overridePath := "/env/explicit.json"

		switch override {
		case 1:
			seeded[uri.File(overridePath)] = []byte(`{"printWidth": 111}`)
		case 2:
			seeded[uri.File(overridePath)] = []byte(`{"tabWidth": 0}`)
		}

		if override > 0 {
			outer.Setenv("THRIFT_LS_CONFIG", overridePath)
		} else {
			outer.Setenv("THRIFT_LS_CONFIG", "")
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

// TestProjectViewConfigLayering is the layering model at the server seam:
// defaults, then Frontend.Defaults, then the discovered document, then CLI,
// and a loader's non-empty include paths override both later layers.
func TestProjectViewConfigLayering(t *testing.T) {
	t.Setenv("THRIFT_LS_CONFIG", "")

	rapid.Check(t, func(t *rapid.T) {
		rootPath := rapid.SampledFrom([]string{"/proj", "/proj/sub", "/proj/sub/deeper"}).Draw(t, "root")

		frontend := drawPropertyPatch(t, "frontend")
		document := drawPropertyPatch(t, "document")
		cli := drawPropertyPatch(t, "cli")

		// The document may sit at the root or one level up, and may be
		// malformed: then it is skipped and an issue diagnostic appears.
		docDepth := rapid.IntRange(0, 2).Draw(t, "docDepth")
		docPath := rootPath
		for i := 0; i < docDepth; i++ {
			docPath = filepath.Dir(docPath)
		}

		docURI := uri.File(filepath.Join(docPath, options.ConfigFileName))
		broken := rapid.IntRange(0, 5).Draw(t, "broken") == 0

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

		want := modelLayered(options.Default(), layers...)
		got := srv.folderConfig(uri.File(rootPath))

		require.Equal(t, patchFields(want), patchFields(got), "effective options for %s", rootPath)

		if broken {
			msgs := diagMessages(client.last(docURI))
			require.Len(t, msgs, 1, "a rejected document is reported at its own path")
			require.Contains(t, msgs[0], "invalid configuration")
		}

		// A loader's include paths are authoritative over the document and
		// CLI, and empty ones fall back to them.
		loaderIncludes := rapid.SampledFrom([][]string{nil, {"/from-build-system"}, {"/one", "/two"}}).Draw(t, "loaderIncludes")

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

// TestLSPSettingsContract checks that lspSettings drops the path extension,
// rejects unknown keys, and keeps the previous settings after a rejection.
func TestLSPSettingsContract(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		patch := drawPropertyPatch(t, "settings")

		doc := patchDocument(patch)
		doc["path"] = rapid.SampledFrom([]string{"/usr/bin/thrift-ls", "", "/tmp/bin"}).Draw(t, "path")

		got, err := lspSettings(mustJSON(t, doc))
		require.NoError(t, err)
		require.Equal(t, patchFields(patch), patchFields(*got), "settings round-trip, path dropped")

		// Unknown keys are rejected, never silently ignored: a typo must not
		// format with stale options.
		unknown := rapid.SampledFrom([]string{"printWhidt", "includePath", "lintt", "algin", "tabwidht"}).Draw(t, "unknown")
		bad := patchDocument(patch)
		bad["path"] = "/bin/x"
		bad[unknown] = rapid.SampledFrom([]any{100, "field", true, []any{"x"}}).Draw(t, "unknownValue")

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
