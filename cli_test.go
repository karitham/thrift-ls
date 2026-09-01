package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runCLI executes the root command with args, capturing its writers.
func runCLI(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	cmd := rootCommand()
	var out, errOut bytes.Buffer
	cmd.Writer = &out
	cmd.ErrWriter = &errOut

	err := cmd.Run(t.Context(), append([]string{"thrift-ls"}, args...))

	return out.String(), errOut.String(), err
}

// Test_FormatCli_GoldenFields pins the indent and align combinations on a
// struct body against the golden outputs in tests/e2e/fields.
func Test_FormatCli_GoldenFields(t *testing.T) {
	cases := []struct {
		name, indent, align string
	}{
		{"2spaces.assign", "  ", "assign"},
		{"2spaces.disable", "  ", "disable"},
		{"4spaces.field", "    ", "field"},
		{"tab.assign", "\t", "assign"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stdout, stderr, err := runCLI(t, "format", "-indent", c.indent, "-align", c.align, "tests/e2e/fields/fields.thrift")
			require.NoError(t, err)
			assert.Empty(t, stderr)
			assert.Equal(t, readGolden(t, "tests/e2e/fields/"+c.name+".expect"), stdout)
		})
	}
}

// Test_FormatCli_GoldenAnnotations runs the formatter over the structured
// annotation fixture (tests/e2e/annotations), which exercises @Name
// <value> annotations on definitions, fields, functions, and throws
// entries across value forms (map, list, parenthesized scalar).
func Test_FormatCli_GoldenAnnotations(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"annotations", nil},
		{"annotations.comma", []string{"-struct-separator", "comma"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := append([]string{"format"}, c.args...)
			args = append(args, "tests/e2e/annotations/annotations.thrift")

			stdout, stderr, err := runCLI(t, args...)
			require.NoError(t, err)
			assert.Empty(t, stderr)
			assert.Equal(t, readGolden(t, "tests/e2e/annotations/"+c.name+".expect"), stdout)
		})
	}
}

// Test_FormatCli_GoldenFieldSeparators runs the struct separator modes on
// the comma-less fields fixture. The golden names keep the flag values of
// the CLI they were recorded against: add, remove, disable.
func Test_FormatCli_GoldenFieldSeparators(t *testing.T) {
	cases := []struct {
		name, sep string
	}{
		{"add", "comma"},
		{"remove", "none"},
		{"disable", "preserve"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stdout, stderr, err := runCLI(t, "format", "-struct-separator", c.sep, "tests/e2e/field_line_comma/fields.thrift")
			require.NoError(t, err)
			assert.Empty(t, stderr)
			assert.Equal(t, readGolden(t, "tests/e2e/field_line_comma/"+c.name+".expect"), stdout)
		})
	}
}

// Test_FormatCli_GoldenEnums covers indent, align, and enum separator
// combinations on tests/e2e/enums.
func Test_FormatCli_GoldenEnums(t *testing.T) {
	cases := []struct {
		name, indent, align, sep string
	}{
		{"2spaces.assign.add", "  ", "assign", "comma"},
		{"2spaces.disable.remove", "  ", "disable", "none"},
		{"4spaces.field.disable", "    ", "field", "preserve"},
		{"tab.assign.disable", "\t", "assign", "preserve"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stdout, stderr, err := runCLI(t, "format",
				"-indent", c.indent, "-align", c.align, "-enum-separator", c.sep,
				"tests/e2e/enums/enums.thrift")
			require.NoError(t, err)
			assert.Empty(t, stderr)
			assert.Equal(t, readGolden(t, "tests/e2e/enums/"+c.name+".expect"), stdout)
		})
	}
}

// Test_CheckCLI_MadeInAbyss runs the check command over the corpus and
// pins the diagnostic counts, including the per-file breakdown and the
// exit code gating on error-severity diagnostics.
func Test_CheckCLI_MadeInAbyss(t *testing.T) {
	stdout, stderr, err := runCLI(t, "check", "tests/made-in-abyss")
	require.Error(t, err)
	assert.Empty(t, stderr)

	assert.Contains(t, stdout, "lints.thrift:")
	assert.Contains(t, stdout, "cycle_a.thrift:")
	assert.Contains(t, stdout, "cycle_b.thrift:")

	errCount := strings.Count(stdout, "  error  ")
	warnCount := strings.Count(stdout, "  warning  ")
	assert.Equal(t, 19, errCount, "error diagnostics")
	assert.Equal(t, 9, warnCount, "warning diagnostics (6 lints + 3 cycles)")

	assert.Contains(t, err.Error(), "19 error(s)")
	assert.Contains(t, err.Error(), "9 warning(s)")
}

// Test_DumpIncludesCLI runs dump --includes over a file whose include
// matches two sibling include roots, and checks the report names both.
func Test_DumpIncludesCLI(t *testing.T) {
	folder := t.TempDir()

	content := "include \"recipes/stew.thrift\"\nstruct Party { 1: i32 members }\n"
	stew := "struct Monster {}\n"

	require.NoError(t, os.MkdirAll(filepath.Join(folder, "laios", "kitchen", "recipes"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(folder, "senshi", "kitchen", "recipes"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(folder, "camp"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(folder, "laios", "kitchen", "recipes", "stew.thrift"), []byte(stew), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(folder, "senshi", "kitchen", "recipes", "stew.thrift"), []byte(stew), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(folder, "camp", "main.thrift"), []byte(content), 0o644))

	configPath := filepath.Join(folder, "thrift-ls.json")
	require.NoError(t, os.WriteFile(configPath, []byte(`{"includePaths": ["laios/kitchen", "senshi/kitchen"]}`), 0o644))

	stdout, stderr, err := runCLI(t, "dump", "--includes", "--config", configPath, filepath.Join(folder, "camp", "main.thrift"))
	require.NoError(t, err)
	assert.Empty(t, stderr)

	assert.Contains(t, stdout, "recipes/stew.thrift")
	assert.Contains(t, stdout, filepath.Join(folder, "senshi", "kitchen", "recipes", "stew.thrift"))
	assert.Contains(t, stdout, filepath.Join(folder, "laios", "kitchen", "recipes", "stew.thrift"))
}

// readGolden returns the recorded output of a CLI test case.
func readGolden(t *testing.T, path string) string {
	t.Helper()

	b, err := os.ReadFile(filepath.FromSlash(path))
	require.NoError(t, err)

	return string(b)
}
