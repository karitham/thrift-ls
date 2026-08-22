package options

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindConfig(t *testing.T) {
	dir := t.TempDir()

	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	// No config anywhere.
	got, err := FindConfig(sub)
	if err != nil || got != "" {
		t.Fatalf("FindConfig = %q, %v; want empty", got, err)
	}

	// Config in an ancestor directory is found walking up.
	cfgPath := filepath.Join(dir, "thrift-ls.json")
	if err := os.WriteFile(cfgPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err = FindConfig(sub)
	if err != nil || got != cfgPath {
		t.Fatalf("FindConfig = %q, %v; want %q", got, err, cfgPath)
	}

	// A nearer config wins.
	near := filepath.Join(dir, "a", "thrift-ls.json")
	if err := os.WriteFile(near, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err = FindConfig(sub)
	if err != nil || got != near {
		t.Fatalf("FindConfig = %q, %v; want %q", got, err, near)
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
