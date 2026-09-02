package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/options"
	"github.com/karitham/thrift-ls/sema"
)

// Test_CheckMadeInAbyss pins the lint corpus: tests/made-in-abyss is the
// living spec of the diagnostics, one intentional mistake per section in
// lints.thrift, an include cycle on the cycle_a/b pair, and no
// diagnostics on the clean files.
func Test_CheckMadeInAbyss(t *testing.T) {
	ctx := t.Context()

	files, err := collectThriftFiles("tests/made-in-abyss")
	require.NoError(t, err)

	rootAbs, err := filepath.Abs("tests/made-in-abyss")
	require.NoError(t, err)

	diags, err := checkFiles(ctx, files, rootAbs, nil, sema.Config{})
	require.NoError(t, err)

	// The clean corpus files report nothing.
	for _, name := range []string{"abyss.thrift", "delvers.thrift", "orth.thrift"} {
		assert.Empty(t, diags[corpusAbs(t, name)], "%s must be clean", name)
	}

	// The mutual include cycle: one warning on cycle_a (its include of
	// cycle_b closes the cycle), two on cycle_b.
	cycleA := diags[corpusAbs(t, "cycle_a.thrift")]
	require.Len(t, cycleA, 1)
	assert.Contains(t, cycleA[0].Message, "cycle dependency")

	cycleB := diags[corpusAbs(t, "cycle_b.thrift")]
	require.Len(t, cycleB, 2)
	assert.Contains(t, cycleB[0].Message, "cycle dependency")
	assert.Contains(t, cycleB[1].Message, "cycle dependency")

	// The mistake showcase: 18 errors and 7 warnings.
	lints := diags[corpusAbs(t, "lints.thrift")]
	require.Len(t, lints, 25)

	errs, warns := 0, 0
	for _, d := range lints {
		switch d.Severity {
		case protocol.DiagnosticSeverityError:
			errs++
		case protocol.DiagnosticSeverityWarning:
			warns++
		}
	}
	assert.Equal(t, 18, errs)
	assert.Equal(t, 7, warns)
	for _, d := range lints {
		if strings.Contains(message(d), "map key must be a scalar type") {
			assert.Equal(t, protocol.DiagnosticSeverityWarning, d.Severity)
		}
	}

	// Every intentional mistake is reported.
	for _, msg := range []string{
		`unused include "unused.thrift"`,
		"field id conflict",
		"field id should be a positive integer in [1, 32767]",
		"duplicate enum Reg",
		"duplicate field same_name",
		"duplicate field repeat",
		"duplicate member RIKO",
		"enum value 1 duplicates OZEN",
		"duplicate function descend",
		"duplicate argument depth",
		`duplicate map key "zone1"`,
		"duplicate set value 4",
		"field type doesn't exist",
		"map key must be a scalar type, found struct",
		"STAR_COMPASS has no explicit value (implicitly 0)",
		"UNHEARD_BELL has no explicit value (implicitly 3)",
		"CROSSED_STILLS has no explicit value (implicitly 5)",
	} {
		assert.True(t, hasMessage(lints, msg), "missing diagnostic %q", msg)
	}

	// Two checks on one line: the second `repeat` field conflicts on both
	// its id (FieldIDCheck) and its name (DuplicateCheck).
	var repeatLine uint32
	for _, d := range lints {
		if strings.Contains(message(d), "duplicate field repeat") {
			repeatLine = d.Range.Start.Line
		}
	}

	onLine := diagsOnLine(lints, repeatLine)
	require.Len(t, onLine, 2, "line %d must carry both diagnostics", repeatLine+1)
	assert.Contains(t, onLine[0].Message, "field id conflict")
	assert.Contains(t, onLine[1].Message, "duplicate field repeat")
}

func Test_CollectThriftFiles(t *testing.T) {
	// A single file.
	files, err := collectThriftFiles("tests/made-in-abyss/lints.thrift")
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.True(t, filepath.IsAbs(files[0]))

	// A folder, recursively, in lexical order.
	files, err = collectThriftFiles("tests/made-in-abyss")
	require.NoError(t, err)

	var names []string
	for _, f := range files {
		names = append(names, filepath.Base(f))
	}
	assert.Equal(t, []string{
		"abyss.thrift", "cycle_a.thrift", "cycle_b.thrift",
		"delvers.thrift", "lints.thrift", "orth.thrift", "unused.thrift",
	}, names)

	// A missing path is an error.
	_, err = collectThriftFiles("tests/does-not-exist")
	require.Error(t, err)
}

// corpusAbs is the absolute path of a made-in-abyss corpus file.
func corpusAbs(t *testing.T, name string) string {
	t.Helper()

	p, err := filepath.Abs(filepath.Join("tests", "made-in-abyss", name))
	require.NoError(t, err)

	return p
}

// message is the diagnostic message as plain text.
func message(d protocol.Diagnostic) string {
	return string(d.Message.(protocol.String))
}

// hasMessage reports whether any diagnostic carries msg.
func hasMessage(diags []protocol.Diagnostic, msg string) bool {
	for _, d := range diags {
		if strings.Contains(message(d), msg) {
			return true
		}
	}

	return false
}

// diagsOnLine returns the diagnostics starting on the 0-based line.
func diagsOnLine(diags []protocol.Diagnostic, line uint32) []protocol.Diagnostic {
	var out []protocol.Diagnostic

	for _, d := range diags {
		if d.Range.Start.Line == line {
			out = append(out, d)
		}
	}

	return out
}

// Test_Check_LintConfig pins that thrift-ls.json lint settings reach the
// check pipeline: a disabled analyzer produces no diagnostics, while the
// default configuration reports the unused include.
func Test_Check_LintConfig(t *testing.T) {
	folder := t.TempDir()
	file := filepath.Join(folder, "user.thrift")
	content := "include \"shared.thrift\"\nstruct S { 1: i32 a }\n"
	require.NoError(t, os.WriteFile(file, []byte(content), 0o644))
	t.Setenv("THRIFT_LS_CONFIG", "")

	config := `{"lint": {"disabled": ["UnusedIncludeCheck"]}}`
	configPath := filepath.Join(folder, "thrift-ls.json")
	require.NoError(t, os.WriteFile(configPath, []byte(config), 0o644))

	patch, err := loadConfig(configPath, folder)
	require.NoError(t, err)

	diags, err := checkFiles(t.Context(), []string{file}, folder, nil, lintConfigOf(options.Effective(patch).Lint))
	require.NoError(t, err)
	assert.Empty(t, diags[file])

	// Without the config the warning fires.
	diags, err = checkFiles(t.Context(), []string{file}, folder, nil, sema.Config{})
	require.NoError(t, err)
	assert.True(t, hasMessage(diags[file], "unused include"))
}
