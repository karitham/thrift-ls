package options_test

// Compares Patch layering, parsing, validation, include anchoring, and
// nearest-config discovery against models of their documented contracts.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/karitham/thrift-ls/formatter"
	"github.com/karitham/thrift-ls/internal/proptest"
	"github.com/karitham/thrift-ls/options"
)

// patchFields projects a patch onto comparable values so a failure names the
// field instead of three layers of pointers.
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
		out["indent"] = *p.Indent
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

var (
	alignValues     = []string{"field", "assign", "disable"}
	indentValues    = []string{"  ", "    ", "\t"}
	separatorValues = []string{"comma", "semicolon", "none", "preserve"}
	analyzerNames   = []string{"duplicate", "field-id", "unused-include"}
	severityValues  = []string{"error", "warning", "info", "hint"}
)

// drawPatch draws one partial patch: every field independently set or unset,
// with the per-construct maps partially filled.
func drawPatch(t *proptest.T) options.Patch {
	var patch options.Patch

	patch.PrintWidth = proptest.Ptr(proptest.IntRange(1, 120), true).Draw(t)
	patch.TabWidth = proptest.Ptr(proptest.IntRange(1, 8), true).Draw(t)
	patch.Align = proptest.Ptr(proptest.SampledFrom(alignValues), true).Draw(t)
	patch.LogLevel = proptest.Ptr(proptest.IntRange(-4, 8), true).Draw(t)
	patch.Indent = proptest.Ptr(proptest.Map(proptest.SampledFrom(indentValues), func(value string) formatter.Indent {
		indent, err := formatter.ParseIndentValue(value)
		if err != nil {
			panic(err)
		}

		return indent
	}), true).Draw(t)

	if proptest.Bool().Draw(t) {
		var separators formatter.Separators

		for _, construct := range formatter.AllConstructs {
			if proptest.Bool().Draw(t) {
				separators.Set(construct, new(proptest.SampledFrom(separatorValues).Draw(t)))
			}
		}

		patch.Separators = &separators
	}

	if proptest.Bool().Draw(t) {
		var breaks formatter.Break

		for _, construct := range formatter.AllConstructs {
			if proptest.Bool().Draw(t) {
				breaks.Set(construct, new(proptest.Bool().Draw(t)))
			}
		}

		patch.Break = &breaks
	}

	if proptest.Bool().Draw(t) {
		patch.IncludePaths = proptest.Ptr(proptest.SliceOfN(
			proptest.SampledFrom([]string{"inc", "../shared", "gen", "/abs/inc"}), 0, 3,
		), false).Draw(t)
	}

	if proptest.Bool().Draw(t) {
		lint := options.LintConfig{
			Disabled: proptest.Ptr(proptest.SliceOfN(proptest.SampledFrom(analyzerNames), 0, 2), true).Draw(t),
			Severity: proptest.Ptr(proptest.MapOfN(proptest.SampledFrom(analyzerNames), proptest.SampledFrom(severityValues), 0, 2), true).Draw(t),
		}
		patch.Lint = &lint
	}

	return patch
}

// fold applies the layers in order, which is the production spelling.
func fold(lowest options.Patch, layers []options.Patch) options.Patch {
	out := lowest
	for _, layer := range layers {
		out = layer.Apply(out)
	}

	return out
}

// FuzzPatchApplyLayeringProperty checks the fold against the fieldwise model
// and against two other groupings of the same layers: composing the overlays
// before applying them once, and Effective over a single layer.
func FuzzPatchApplyLayeringProperty(f *testing.F) {
	// Seed bytes are arbitrary: the stream is derived from their hash, so the
	// seeds are picked to draw every layer count 0-4 in a plain run.
	proptest.Fuzz(f, proptest.Corpus{
		[]byte(""),
		[]byte("indent"),
		[]byte("settings"),
		[]byte("four layers"),
		[]byte("many layers"),
		[]byte("separators"),
	}, func(tb *testing.T, seed []byte) {
		t := proptest.New(tb, seed)

		defaults := options.Default()

		count := proptest.IntRange(0, 4).Draw(t)

		layers := make([]options.Patch, count)
		for i := range layers {
			layers[i] = drawPatch(t)
		}

		got := fold(defaults, layers)
		want := proptest.ModelLayered(defaults, layers...)

		require.Equal(t, patchFields(want), patchFields(got), "fieldwise layering, layers %d", count)

		// Grouping: composing the overlays first and applying the composite to
		// the defaults is the same fold.
		var composed options.Patch
		for _, layer := range layers {
			composed = layer.Apply(composed)
		}

		require.Equal(t, patchFields(got), patchFields(composed.Apply(defaults)), "grouping invariance")

		// Idempotence: a patch applied to itself changes nothing.
		for _, layer := range layers {
			require.Equal(t, patchFields(layer), patchFields(layer.Apply(layer)), "apply is idempotent")
		}

		// Effective is the same fold with the defaults at the bottom.
		if count == 1 {
			require.Equal(t, patchFields(got), patchFields(options.Effective(&layers[0])))
		}

		require.Equal(t, patchFields(options.Default()), patchFields(options.Effective(nil)))
	})
}

// document renders the set fields of a patch as a thrift-ls.json body.
func document(patch options.Patch) map[string]any {
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

// FuzzParseRoundTripProperty checks that parsing keeps set fields and rejects
// unknown keys.
func FuzzParseRoundTripProperty(f *testing.F) {
	// Seed bytes are arbitrary: the stream is derived from their hash, so the
	// seeds are picked to draw every unknown key and value in a plain run.
	proptest.Fuzz(f, proptest.Corpus{
		[]byte(""),
		[]byte("four layers"),
		[]byte("root"),
		[]byte("settings"),
		[]byte("patch"),
		[]byte("defaults"),
	}, func(tb *testing.T, seed []byte) {
		t := proptest.New(tb, seed)

		patch := drawPatch(t)

		data, err := json.Marshal(document(patch))
		require.NoError(t, err)

		parsed, err := options.Parse(data)
		require.NoError(t, err)
		require.NotNil(t, parsed)
		require.Equal(t, patchFields(patch), patchFields(*parsed), "document round-trip")

		// Unknown keys are rejected, and the error names the key.
		unknown := proptest.SampledFrom([]string{"printWidthExtra", "indentt", "lintt", "algin", "logLevle"}).Draw(t)
		bad := document(patch)
		bad[unknown] = proptest.SampledFrom([]any{1, "x", true}).Draw(t)

		data, err = json.Marshal(bad)
		require.NoError(t, err)

		_, err = options.Parse(data)
		require.Error(t, err, "unknown key %q must be rejected", unknown)
		require.Contains(t, err.Error(), unknown)
	})
}

// FuzzValidateProperty checks that a generated patch is valid and that
// breaking one field makes it invalid.
func FuzzValidateProperty(f *testing.F) {
	// Seed bytes are arbitrary: the stream is derived from their hash, so the
	// seeds are picked to break every field in turn in a plain run.
	proptest.Fuzz(f, proptest.Corpus{
		[]byte("four layers"),
		[]byte("root"),
		[]byte("settings"),
		[]byte("patch"),
		[]byte("defaults"),
		[]byte("separators"),
	}, func(tb *testing.T, seed []byte) {
		t := proptest.New(tb, seed)

		patch := drawPatch(t)
		require.NoError(t, patch.Validate(), "a generated patch must be valid")

		broken := patch

		switch proptest.IntRange(0, 4).Draw(t) {
		case 0:
			broken.PrintWidth = new(0)
		case 1:
			broken.TabWidth = new(-2)
		case 2:
			broken.Align = new("diagonal")
		case 3:
			var separators formatter.Separators
			separators.Structs = new("comma-comma")
			broken.Separators = &separators
		case 4:
			severity := map[string]string{"duplicate": "fatal"}
			lint := options.LintConfig{Severity: &severity}
			broken.Lint = &lint
		}

		require.Error(t, broken.Validate(), "one broken field must fail validation")
	})
}

// FuzzAnchorIncludesProperty checks relative and absolute include paths and
// that anchoring twice matches anchoring once.
func FuzzAnchorIncludesProperty(f *testing.F) {
	// Seed bytes are arbitrary: the stream is derived from their hash, so the
	// seeds are picked to draw every include directory and path shape.
	proptest.Fuzz(f, proptest.Corpus{
		[]byte("layer"),
		[]byte("layers"),
		[]byte("separators"),
		[]byte("settings"),
		[]byte("overrides"),
		[]byte("include paths"),
	}, func(tb *testing.T, seed []byte) {
		t := proptest.New(tb, seed)

		dir := proptest.SampledFrom([]string{"/cfg", "/cfg/a/b", "/"}).Draw(t)
		paths := proptest.SliceOfN(proptest.SampledFrom([]string{"inc", "../up", "./here", "/abs/inc", "a/b"}), 0, 3).Draw(t)

		patch := options.Patch{IncludePaths: &paths}
		options.AnchorIncludes(&patch, dir)

		want := make([]string, 0, len(paths))
		for _, path := range paths {
			if filepath.IsAbs(path) {
				want = append(want, path)

				continue
			}

			want = append(want, filepath.Join(dir, path))
		}

		require.Equal(t, want, *patch.IncludePaths)

		options.AnchorIncludes(&patch, "/elsewhere")
		require.Equal(t, want, *patch.IncludePaths, "anchoring is idempotent")

		unset := options.Patch{}
		options.AnchorIncludes(&unset, dir)
		require.Nil(t, unset.IncludePaths, "an unset include list stays unset")
	})
}

// FuzzFindNearestConfigProperty checks the walk-up, absolute results, and the
// THRIFT_LS_CONFIG override.
func FuzzFindNearestConfigProperty(f *testing.F) {
	// Seed bytes are arbitrary: the stream is derived from their hash, so the
	// seeds are picked to draw every walk-up depth 0-3 in a plain run.
	proptest.Fuzz(f, proptest.Corpus{
		[]byte("indent"),
		[]byte("layers"),
		[]byte("config"),
		[]byte("layer"),
		[]byte("root only"),
		[]byte("walk up"),
	}, func(tb *testing.T, seed []byte) {
		t := proptest.New(tb, seed)

		root := tb.TempDir()
		segments := []string{"alpha", "beta", "gamma"}

		dir := root

		depth := proptest.IntRange(0, 3).Draw(t)
		for i := 0; i < depth; i++ {
			dir = filepath.Join(dir, segments[i])
		}

		require.NoError(t, os.MkdirAll(dir, 0o755))

		ancestors := make([]string, 0, depth+1)
		for current := dir; ; current = filepath.Dir(current) {
			ancestors = append(ancestors, current)
			if current == root {
				break
			}
		}

		want := ""

		// Always plant a document at the case root so the asserted answer is
		// inside the temp tree even if the machine has one above it.
		planted := map[int]bool{len(ancestors) - 1: true}

		for i := range ancestors {
			if proptest.Bool().Draw(t) {
				planted[i] = true
			}
		}

		for i := range ancestors {
			if !planted[i] {
				continue
			}

			path := filepath.Join(ancestors[i], options.ConfigFileName)
			require.NoError(t, os.WriteFile(path, []byte(`{"printWidth": 100}`), 0o644))

			if want == "" {
				want = path
			}
		}

		require.Equal(t, filepath.ToSlash(want), filepath.ToSlash(mustNearest(t, dir)))

		// THRIFT_LS_CONFIG replaces the search for FindConfig but does not
		// change FindNearestConfig.
		override := filepath.Join(root, "override.json")
		t.Setenv("THRIFT_LS_CONFIG", override)

		require.Equal(t, filepath.ToSlash(want), filepath.ToSlash(mustNearest(t, dir)))

		got, err := options.FindConfig(dir)
		require.NoError(t, err)
		require.Equal(t, override, got)

		t.Setenv("THRIFT_LS_CONFIG", "")
	})
}

func mustNearest(t require.TestingT, dir string) string {
	path, err := options.FindNearestConfig(dir)
	require.NoError(t, err)

	return path
}

// TestParseRejectsCaseOnlyKeyTypos checks that Parse rejects keys that differ
// from struct fields only by case.
func TestParseRejectsCaseOnlyKeyTypos(t *testing.T) {
	for _, tt := range []struct {
		name string
		doc  string
	}{
		{"lowercase", `{"printwidth": 100}`},
		{"uppercase", `{"PRINTWIDTH": 100}`},
		{"field name", `{"IncludePaths": ["inc"]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			patch, err := options.Parse([]byte(tt.doc))
			require.Error(t, err)
			require.Nil(t, patch)
		})
	}
}
