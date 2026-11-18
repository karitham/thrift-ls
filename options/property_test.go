package options_test

// Compares Patch layering, parsing, validation, include anchoring, and
// nearest-config discovery against models of their documented contracts.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/karitham/thrift-ls/formatter"
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
func drawPatch(t *rapid.T, name string) options.Patch {
	var patch options.Patch

	patch.PrintWidth = rapid.Ptr(rapid.IntRange(1, 120), true).Draw(t, name+".printWidth")
	patch.TabWidth = rapid.Ptr(rapid.IntRange(1, 8), true).Draw(t, name+".tabWidth")
	patch.Align = rapid.Ptr(rapid.SampledFrom(alignValues), true).Draw(t, name+".align")
	patch.LogLevel = rapid.Ptr(rapid.IntRange(-4, 8), true).Draw(t, name+".logLevel")
	patch.Indent = rapid.Ptr(rapid.Map(rapid.SampledFrom(indentValues), func(value string) formatter.Indent {
		indent, err := formatter.ParseIndentValue(value)
		if err != nil {
			panic(err)
		}

		return indent
	}), true).Draw(t, name+".indent")

	if rapid.Bool().Draw(t, name+".separatorsSet") {
		var separators formatter.Separators

		for _, construct := range formatter.AllConstructs {
			if rapid.Bool().Draw(t, name+".separator."+construct.String()) {
				separators.Set(construct, new(rapid.SampledFrom(separatorValues).Draw(t, name+".separatorValue")))
			}
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

	if rapid.Bool().Draw(t, name+".lintSet") {
		lint := options.LintConfig{
			Disabled: rapid.Ptr(rapid.SliceOfN(rapid.SampledFrom(analyzerNames), 0, 2), true).Draw(t, name+".lint.disabled"),
			Severity: rapid.Ptr(rapid.MapOfN(rapid.SampledFrom(analyzerNames), rapid.SampledFrom(severityValues), 0, 2), true).Draw(t, name+".lint.severity"),
		}
		patch.Lint = &lint
	}

	return patch
}

// modelLayered folds the layers the way the precedence comment describes:
// each field independently takes its value from the highest layer that sets
// it, and per-construct maps resolve construct by construct.
func modelLayered(lowest options.Patch, layers ...options.Patch) options.Patch {
	all := append([]options.Patch{lowest}, layers...) // ascending precedence

	var out options.Patch

	for i := len(all) - 1; i >= 0; i-- {
		layer := all[i]

		if out.PrintWidth == nil {
			out.PrintWidth = layer.PrintWidth
		}

		if out.TabWidth == nil {
			out.TabWidth = layer.TabWidth
		}

		if out.Align == nil {
			out.Align = layer.Align
		}

		if out.Indent == nil {
			out.Indent = layer.Indent
		}

		if out.IncludePaths == nil {
			out.IncludePaths = layer.IncludePaths
		}

		if out.LogLevel == nil {
			out.LogLevel = layer.LogLevel
		}

		if out.Lint == nil {
			out.Lint = layer.Lint
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

			if value := src.Get(construct); value != unset {
				out.Set(construct, value)
			}
		}
	}

	return out
}

// fold applies the layers in order, which is the production spelling.
func fold(lowest options.Patch, layers []options.Patch) options.Patch {
	out := lowest
	for _, layer := range layers {
		out = layer.Apply(out)
	}

	return out
}

// TestPatchApplyLayeringProperty checks the fold against the fieldwise model
// and against two other groupings of the same layers: composing the overlays
// before applying them once, and Effective over a single layer.
func TestPatchApplyLayeringProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		defaults := options.Default()

		count := rapid.IntRange(0, 4).Draw(t, "layers")

		layers := make([]options.Patch, count)
		for i := range layers {
			layers[i] = drawPatch(t, fmt.Sprintf("layer%d", i))
		}

		got := fold(defaults, layers)
		want := modelLayered(defaults, layers...)

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

// TestParseRoundTripProperty checks that parsing keeps set fields and rejects
// unknown keys.
func TestParseRoundTripProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		patch := drawPatch(t, "patch")

		data, err := json.Marshal(document(patch))
		require.NoError(t, err)

		parsed, err := options.Parse(data)
		require.NoError(t, err)
		require.NotNil(t, parsed)
		require.Equal(t, patchFields(patch), patchFields(*parsed), "document round-trip")

		// Unknown keys are rejected, and the error names the key.
		unknown := rapid.SampledFrom([]string{"printWidthExtra", "indentt", "lintt", "algin", "logLevle"}).Draw(t, "unknown")
		bad := document(patch)
		bad[unknown] = rapid.SampledFrom([]any{1, "x", true}).Draw(t, "unknownValue")

		data, err = json.Marshal(bad)
		require.NoError(t, err)

		_, err = options.Parse(data)
		require.Error(t, err, "unknown key %q must be rejected", unknown)
		require.Contains(t, err.Error(), unknown)
	})
}

// TestValidateProperty checks that a generated patch is valid and that
// breaking one field makes it invalid.
func TestValidateProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		patch := drawPatch(t, "patch")
		require.NoError(t, patch.Validate(), "a generated patch must be valid")

		broken := patch

		switch rapid.IntRange(0, 4).Draw(t, "which") {
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

// TestAnchorIncludesProperty checks relative and absolute include paths and
// that anchoring twice matches anchoring once.
func TestAnchorIncludesProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		dir := rapid.SampledFrom([]string{"/cfg", "/cfg/a/b", "/"}).Draw(t, "dir")
		paths := rapid.SliceOfN(rapid.SampledFrom([]string{"inc", "../up", "./here", "/abs/inc", "a/b"}), 0, 3).Draw(t, "paths")

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

// TestFindNearestConfigProperty checks the walk-up, absolute results, and
// the THRIFT_LS_CONFIG override.
func TestFindNearestConfigProperty(t *testing.T) {
	outer := t
	root := t.TempDir()
	segments := []string{"alpha", "beta", "gamma"}
	cases := 0

	rapid.Check(t, func(t *rapid.T) {
		cases++

		dir := filepath.Join(root, fmt.Sprintf("case%d", cases))

		depth := rapid.IntRange(0, 3).Draw(t, "depth")
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
			if rapid.Bool().Draw(t, fmt.Sprintf("doc%d", i)) {
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
		outer.Setenv("THRIFT_LS_CONFIG", override)

		require.Equal(t, filepath.ToSlash(want), filepath.ToSlash(mustNearest(t, dir)))

		got, err := options.FindConfig(dir)
		require.NoError(t, err)
		require.Equal(t, override, got)

		outer.Setenv("THRIFT_LS_CONFIG", "")
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
