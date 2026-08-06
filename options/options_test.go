package options

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/karitham/thrift-ls/formatter"
)

func TestParseIndentValue(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    Indent
		wantErr bool
	}{
		{"empty defaults", "", Indent{"    ", 4}, false},
		{"literal two spaces", "  ", Indent{"  ", 2}, false},
		{"literal four spaces", "    ", Indent{"    ", 4}, false},
		{"literal tab", "\t", Indent{"\t", 4}, false},
		{"literal two tabs", "\t\t", Indent{"\t\t", 8}, false},
		{"number", "8", Indent{"        ", 8}, false},
		{"number one", "1", Indent{" ", 1}, false},
		{"legacy spaces", "2spaces", Indent{"  ", 2}, false},
		{"legacy space singular", "1space", Indent{" ", 1}, false},
		{"legacy tab", "1tab", Indent{"\t", 4}, false},
		{"legacy bare tab", "tab", Indent{"\t", 4}, false},
		{"legacy two tabs", "2tabs", Indent{"\t\t", 8}, false},
		{"zero number", "0", Indent{}, true},
		{"mixed spaces and tabs", " \t", Indent{}, true},
		{"garbage", "banana", Indent{}, true},
		{"negative", "-2", Indent{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseIndentValue(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestIndentUnmarshal(t *testing.T) {
	tests := []struct {
		name string
		json string
		want Indent
	}{
		{"string spaces", `"  "`, Indent{"  ", 2}},
		{"string tab", `"\t"`, Indent{"\t", 4}},
		{"number", `8`, Indent{"        ", 8}},
		{"legacy", `"2spaces"`, Indent{"  ", 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var i Indent
			if err := json.Unmarshal([]byte(tt.json), &i); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if i != tt.want {
				t.Errorf("got %+v, want %+v", i, tt.want)
			}
		})
	}
}

func TestPatchApply(t *testing.T) {
	base := Default()

	overlay := Patch{}
	printWidth := 100
	overlay.PrintWidth = &printWidth

	got := overlay.Apply(base)
	if got.PrintWidth == nil || *got.PrintWidth != 100 {
		t.Errorf("PrintWidth not overridden: %v", got.PrintWidth)
	}
	if got.Align == nil || *got.Align != "field" {
		t.Errorf("Align should stay from base: %v", got.Align)
	}
}

func TestPatchValidate(t *testing.T) {
	intPtr := func(n int) *int { return &n }
	strPtr := func(s string) *string { return &s }

	tests := []struct {
		name    string
		patch   Patch
		wantErr bool
	}{
		{"default is valid", Default(), false},
		{"bad printWidth", Patch{PrintWidth: intPtr(0)}, true},
		{"bad tabWidth", Patch{TabWidth: intPtr(-1)}, true},
		{"bad align", Patch{Align: strPtr("sideways")}, true},
		{"bad comma", Patch{FieldLineComma: strPtr("maybe")}, true},
		{"preserve alias", Patch{FieldLineComma: strPtr("preserve")}, false},
		{"bad indent value", Patch{Indent: &Indent{Value: "x", Width: 1}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.patch.Validate()
			if tt.wantErr && err == nil {
				t.Error("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestPatchFormatter(t *testing.T) {
	indent := Indent{Value: "  ", Width: 2}
	p := Patch{Indent: &indent, PrintWidth: new(100)}
	o, err := p.Formatter()
	if err != nil {
		t.Fatalf("Formatter: %v", err)
	}
	if o.PrintWidth != 100 || o.Indent != "  " || o.TabWidth != 2 {
		t.Errorf("got %+v", o)
	}
	if o.Align != formatter.AlignField || o.FieldLineComma != formatter.CommaPreserve {
		t.Errorf("defaults wrong: %+v", o)
	}

	comma := "add"
	align := "assign"
	p = Patch{FieldLineComma: &comma, Align: &align}
	o, err = p.Formatter()
	if err != nil {
		t.Fatalf("Formatter: %v", err)
	}
	if o.FieldLineComma != formatter.CommaAdd || o.Align != formatter.AlignAssign {
		t.Errorf("got %+v", o)
	}
}

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
	cfgPath := filepath.Join(dir, "thriftls.json")
	if err := os.WriteFile(cfgPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = FindConfig(sub)
	if err != nil || got != cfgPath {
		t.Fatalf("FindConfig = %q, %v; want %q", got, err, cfgPath)
	}

	// A nearer config wins.
	near := filepath.Join(dir, "a", "thriftls.json")
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
	cfgPath := filepath.Join(dir, "thriftls.json")
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
	if p.FieldLineComma == nil || *p.FieldLineComma != "disable" {
		t.Errorf("comma = %v, want default disable", p.FieldLineComma)
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
	cfgPath := filepath.Join(dir, "thriftls.json")
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
