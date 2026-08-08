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

// readGolden returns the recorded output of a CLI test case.
func readGolden(t *testing.T, path string) string {
	t.Helper()

	b, err := os.ReadFile(filepath.FromSlash(path))
	require.NoError(t, err)

	return string(b)
}
