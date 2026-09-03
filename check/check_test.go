package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karitham/thrift-ls/options"
)

// corpusDir is the shared lint corpus, one intentional mistake per section
// in lints.thrift, an include cycle on the cycle_a/b pair, and no
// diagnostics on the clean files.
func corpusDir(t *testing.T) string {
	t.Helper()

	abs, err := filepath.Abs(filepath.Join("..", "tests", "made-in-abyss"))
	require.NoError(t, err)

	return abs
}

// corpusFiles returns the absolute paths of the corpus files in lexical
// order.
func corpusFiles(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(corpusDir(t))
	require.NoError(t, err)

	var files []string

	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".thrift") {
			files = append(files, filepath.Join(corpusDir(t), e.Name()))
		}
	}

	return files
}

// Test_CheckMadeInAbyss pins the lint corpus through the public boundary:
// the mistake showcase, the include cycle, and the clean files.
func Test_CheckMadeInAbyss(t *testing.T) {
	ctx := t.Context()
	root := corpusDir(t)
	files := corpusFiles(t)

	result, err := Run(ctx, Request{Files: files, Folder: root})
	require.NoError(t, err)

	diags := result.Diagnostics
	assert.Nil(t, result.Fix)

	// The clean corpus files report nothing.
	for _, name := range []string{"abyss.thrift", "delvers.thrift", "orth.thrift"} {
		assert.Empty(t, diags[filepath.Join(root, name)], "%s must be clean", name)
	}

	// The mutual include cycle: one warning on cycle_a (its include of
	// cycle_b closes the cycle), two on cycle_b.
	cycleA := diags[filepath.Join(root, "cycle_a.thrift")]
	require.Len(t, cycleA, 1)
	assert.Contains(t, cycleA[0].Message, "cycle dependency")

	cycleB := diags[filepath.Join(root, "cycle_b.thrift")]
	require.Len(t, cycleB, 2)
	assert.Contains(t, cycleB[0].Message, "cycle dependency")
	assert.Contains(t, cycleB[1].Message, "cycle dependency")

	// The mistake showcase: 18 errors and 7 warnings.
	lints := diags[filepath.Join(root, "lints.thrift")]
	require.Len(t, lints, 25)

	errs, warns := 0, 0
	for _, d := range lints {
		switch d.Severity {
		case SeverityError:
			errs++
		case SeverityWarning:
			warns++
		}
	}
	assert.Equal(t, 18, errs)
	assert.Equal(t, 7, warns)
	for _, d := range lints {
		if strings.Contains(d.Message, "map key must be a scalar type") {
			assert.Equal(t, SeverityWarning, d.Severity)
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
	var repeatLine int
	for _, d := range lints {
		if strings.Contains(d.Message, "duplicate field repeat") {
			repeatLine = d.Line
		}
	}

	onLine := diagsOnLine(lints, repeatLine)
	require.Len(t, onLine, 2, "line %d must carry both diagnostics", repeatLine)
	assert.Contains(t, onLine[0].Message, "field id conflict")
	assert.Contains(t, onLine[1].Message, "duplicate field repeat")
}

// hasMessage reports whether any diagnostic carries msg.
func hasMessage(diags []Diagnostic, msg string) bool {
	for _, d := range diags {
		if strings.Contains(d.Message, msg) {
			return true
		}
	}

	return false
}

// diagsOnLine returns the diagnostics starting on the 1-based line.
func diagsOnLine(diags []Diagnostic, line int) []Diagnostic {
	var out []Diagnostic

	for _, d := range diags {
		if d.Line == line {
			out = append(out, d)
		}
	}

	return out
}

// Test_Check_LintConfig pins that lint settings reach the pipeline: a
// disabled analyzer produces no diagnostics, while the default
// configuration reports the unused include.
func Test_Check_LintConfig(t *testing.T) {
	folder := t.TempDir()
	file := filepath.Join(folder, "user.thrift")
	content := "include \"shared.thrift\"\nstruct S { 1: i32 a }\n"
	require.NoError(t, os.WriteFile(file, []byte(content), 0o644))
	t.Setenv("THRIFT_LS_CONFIG", "")

	config := `{"lint": {"disabled": ["UnusedIncludeCheck"]}}`
	configPath := filepath.Join(folder, "thrift-ls.json")
	require.NoError(t, os.WriteFile(configPath, []byte(config), 0o644))

	patch, err := options.Load(configPath)
	require.NoError(t, err)

	result, err := Run(t.Context(), Request{Files: []string{file}, Folder: folder, Lint: options.Effective(patch).Lint})
	require.NoError(t, err)
	assert.Empty(t, result.Diagnostics[file])

	// Without the config the warning fires.
	result, err = Run(t.Context(), Request{Files: []string{file}, Folder: folder})
	require.NoError(t, err)
	assert.True(t, hasMessage(result.Diagnostics[file], "unused include"))
}

func Test_Check_FixConverges(t *testing.T) {
	folder := t.TempDir()
	file := filepath.Join(folder, "user.thrift")
	require.NoError(t, os.WriteFile(file, []byte("enum Color {\n  RED,\n  GREEN = 2,\n  BLUE,\n}\n"), 0o644))

	result, err := Run(t.Context(), Request{Files: []string{file}, Folder: folder, Fix: true})
	require.NoError(t, err)
	require.NotNil(t, result.Fix, "a fix run reports its summary")
	assert.Positive(t, result.Fix.Applied)
	assert.Positive(t, result.Fix.Passes)
	assert.Contains(t, result.Fix.Files, file)

	for _, d := range result.Diagnostics[file] {
		assert.NotContains(t, d.Message, "explicit value", "remaining diagnostics hold only what fixes cannot do")
	}

	fixed, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Contains(t, string(fixed), "RED = 0")
}

func Test_Check_EmptyInput(t *testing.T) {
	result, err := Run(t.Context(), Request{})
	require.NoError(t, err)
	assert.NotNil(t, result.Diagnostics)
	assert.Empty(t, result.Diagnostics)
}

func Test_Check_SeverityOverride(t *testing.T) {
	folder := t.TempDir()
	file := filepath.Join(folder, "user.thrift")
	require.NoError(t, os.WriteFile(file, []byte("include \"shared.thrift\"\nstruct S { 1: i32 a }\n"), 0o644))

	sev := map[string]string{"unused-include": "error"}
	result, err := Run(t.Context(), Request{
		Files:  []string{file},
		Folder: folder,
		Lint:   &options.LintConfig{Severity: &sev},
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.Diagnostics[file])
	for _, d := range result.Diagnostics[file] {
		if d.Code == "unused-include" {
			assert.Equal(t, SeverityError, d.Severity)
		}
	}
}
