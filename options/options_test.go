package options

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindNearestConfig(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "a", "b")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	t.Setenv("THRIFT_LS_CONFIG", filepath.Join(dir, "hostile.json"))

	got, err := FindNearestConfig(sub)
	require.NoError(t, err)
	assert.Empty(t, got)

	cfgPath := filepath.Join(dir, "thrift-ls.json")
	require.NoError(t, os.WriteFile(cfgPath, []byte("{}"), 0o644))

	got, err = FindNearestConfig(sub)
	require.NoError(t, err)
	assert.Equal(t, cfgPath, got)
	assert.True(t, filepath.IsAbs(got))

	near := filepath.Join(dir, "a", "thrift-ls.json")
	require.NoError(t, os.WriteFile(near, []byte("{}"), 0o644))

	got, err = FindNearestConfig(sub)
	require.NoError(t, err)
	assert.Equal(t, near, got)
}

func TestFindNearestConfigReturnsAbsolutePathForRelativeDirectory(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ConfigFileName)
	require.NoError(t, os.WriteFile(cfgPath, []byte("{}"), 0o644))

	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	defer func() { _ = os.Chdir(wd) }()

	got, err := FindNearestConfig(".")
	require.NoError(t, err)
	assert.Equal(t, cfgPath, got)
}

func TestFindConfigHonorsEnvironment(t *testing.T) {
	dir := t.TempDir()
	nearest := filepath.Join(dir, ConfigFileName)
	environment := filepath.Join(dir, "environment.json")
	require.NoError(t, os.WriteFile(nearest, []byte("{}"), 0o644))
	require.NoError(t, os.WriteFile(environment, []byte("{}"), 0o644))
	t.Setenv("THRIFT_LS_CONFIG", "")

	got, err := FindConfig(dir)
	require.NoError(t, err)
	assert.Equal(t, nearest, got)

	t.Setenv("THRIFT_LS_CONFIG", environment)

	got, err = FindConfig(dir)
	require.NoError(t, err)
	assert.Equal(t, environment, got)
}

// A relative THRIFT_LS_CONFIG also comes back absolute.
func TestFindConfigRelativeEnv(t *testing.T) {
	dir := t.TempDir()

	cfgPath := filepath.Join(dir, "thrift-ls.json")
	if err := os.WriteFile(cfgPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("THRIFT_LS_CONFIG", "thrift-ls.json")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(wd) }()

	got, err := FindConfig(".")
	if err != nil || got != cfgPath {
		t.Fatalf("FindConfig = %q, %v; want %q", got, err, cfgPath)
	}
}

// A relative config path anchors its include paths to the config's
// directory, not the process CWD.
func TestLoadRelativePathAnchorsIncludePaths(t *testing.T) {
	dir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(dir, "dungeon"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(dir, "thrift-ls.json")
	if err := os.WriteFile(cfgPath, []byte(`{"includePaths": ["dungeon"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(wd) }()

	cfg, err := Load("thrift-ls.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.IncludePaths == nil || len(*cfg.IncludePaths) != 1 {
		t.Fatalf("includePaths = %v", cfg.IncludePaths)
	}

	if got := (*cfg.IncludePaths)[0]; got != filepath.Join(dir, "dungeon") {
		t.Errorf("includePaths[0] = %q, want %q", got, filepath.Join(dir, "dungeon"))
	}
}

func TestLoadAndEffective(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "thrift-ls.json")

	content := `{
  "printWidth": 100,
  "indent": "  ",
  "align": "disable"
}`
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.PrintWidth == nil || *cfg.PrintWidth != 100 {
		t.Errorf("printWidth = %v, want 100", cfg.PrintWidth)
	}

	if cfg.Indent == nil || cfg.Indent.Value != "  " {
		t.Errorf("indent = %+v, want two spaces", cfg.Indent)
	}

	p := Effective(cfg)
	if p.PrintWidth == nil || *p.PrintWidth != 100 {
		t.Errorf("effective printWidth = %v, want 100", p.PrintWidth)
	}
	// Unset config fields keep their defaults.
	if p.Separators == nil || p.Separators.Structs == nil || *p.Separators.Structs != "preserve" {
		t.Errorf("comma = %v, want default preserve", p.Separators)
	}

	if p.Indent == nil || p.Indent.Value != "  " {
		t.Errorf("indent = %+v, want two spaces", p.Indent)
	}

	// Effective with no config returns plain defaults.
	d := Effective(nil)
	if d.PrintWidth == nil || *d.PrintWidth != 80 {
		t.Errorf("default printWidth = %v, want 80", d.PrintWidth)
	}

	if d.Indent == nil || d.Indent.Value != "    " {
		t.Errorf("default indent = %+v, want four spaces", d.Indent)
	}
}

func TestLoadRejectsUnknownOverrideKeys(t *testing.T) {
	// Config files written for the old overrides feature should fail loudly
	// rather than silently ignoring per-file settings.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "thrift-ls.json")

	content := `{
  "printWidth": 100,
  "overrides": [
    { "files": ["**/gen/**"], "options": { "printWidth": 120 } }
  ]
}`
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(cfgPath); err == nil {
		t.Fatal("Load accepted a config with an overrides key")
	}
}

// TestLintConfigDecodeAndConvert pins the lint config: JSON decoding,
// typo rejection, and the plain-data shape the server converts.
func TestLintConfigDecodeAndConvert(t *testing.T) {
	tests := []struct {
		name         string
		data         string
		wantErr      bool
		wantEnabled  bool
		wantDisabled []string
		wantSeverity map[string]string
	}{
		{
			name:         "disabled analyzers and severity overrides",
			data:         `{"lint": {"disabled": ["unused-include"], "severity": {"implicit-enum-value": "info"}}}`,
			wantEnabled:  true,
			wantDisabled: []string{"unused-include"},
			wantSeverity: map[string]string{"implicit-enum-value": "info"},
		},
		{
			name:    "unknown severity is rejected",
			data:    `{"lint": {"severity": {"unused-include": "loud"}}}`,
			wantErr: true,
		},
		{
			name:    "unknown lint key is rejected",
			data:    `{"lint": {"disabledz": []}}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Parse([]byte(tt.data))
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, p.Lint)

			if tt.wantDisabled != nil {
				require.NotNil(t, p.Lint.Disabled)
				assert.Equal(t, tt.wantDisabled, *p.Lint.Disabled)
			}

			if tt.wantSeverity != nil {
				require.NotNil(t, p.Lint.Severity)
				assert.Equal(t, tt.wantSeverity, *p.Lint.Severity)
			}
		})
	}
}
