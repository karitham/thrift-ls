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
		{"mixed spaces and tabs", " \t", Indent{}, true},
		{"garbage", "banana", Indent{}, true},
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
		{"bad comma", Patch{Separators: &Separators{Structs: strPtr("maybe")}}, true},
		{"preserve alias", Patch{Separators: &Separators{Structs: strPtr("preserve")}}, false},
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

	if o.Align != formatter.AlignField || o.Separator.Get(formatter.ConstructStruct) != formatter.SeparatorPreserve {
		t.Errorf("defaults wrong: %+v", o)
	}

	comma := "comma"
	align := "assign"
	p = Patch{Separators: &Separators{Structs: &comma}, Align: &align}

	o, err = p.Formatter()
	if err != nil {
		t.Fatalf("Formatter: %v", err)
	}

	if o.Separator.Get(formatter.ConstructStruct) != formatter.SeparatorComma || o.Align != formatter.AlignAssign {
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

// TestPatchSeparatorModes maps every config value to the formatter modes.
func TestPatchSeparatorModes(t *testing.T) {
	tests := []struct {
		value    string
		field    formatter.SeparatorMode
		function formatter.SeparatorMode
	}{
		{"comma", formatter.SeparatorComma, formatter.SeparatorComma},
		{"none", formatter.SeparatorNone, formatter.SeparatorNone},
		{"semicolon", formatter.SeparatorSemicolon, formatter.SeparatorSemicolon},
		{"preserve", formatter.SeparatorPreserve, formatter.SeparatorPreserve},
		{"preserve", formatter.SeparatorPreserve, formatter.SeparatorPreserve},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			p := Patch{Separators: &Separators{
				Structs: &tt.value, Unions: &tt.value, Exceptions: &tt.value,
				Enums: &tt.value, Arguments: &tt.value, Throws: &tt.value,
				Lists: &tt.value, Maps: &tt.value,
			}}

			o, err := p.Formatter()
			if err != nil {
				t.Fatalf("Formatter: %v", err)
			}

			for _, c := range formatter.AllConstructs {
				if o.Separator.Get(c) != tt.field {
					t.Errorf("value %q: construct %s = %v, want %v", tt.value, c, o.Separator.Get(c), tt.field)
				}
			}
		})
	}

	// The two options map independently.
	semicolon, comma := "semicolon", "comma"
	p := Patch{Separators: &Separators{Structs: &semicolon, Enums: &semicolon, Arguments: &comma, Throws: &comma}}

	o, err := p.Formatter()
	if err != nil {
		t.Fatalf("Formatter: %v", err)
	}

	if o.Separator.Get(formatter.ConstructStruct) != formatter.SeparatorSemicolon || o.Separator.Get(formatter.ConstructArguments) != formatter.SeparatorComma {
		t.Errorf("independent mapping failed: %+v", o)
	}
}

// TestPatchBreak maps the break group to the formatter options.
func TestPatchBreak(t *testing.T) {
	trueVal, falseVal := true, false

	p := Patch{Break: &Break{Structs: &trueVal, Enums: &falseVal}}

	o, err := p.Formatter()
	if err != nil {
		t.Fatalf("Formatter: %v", err)
	}

	if !o.Break.Get(formatter.ConstructStruct) || o.Break.Get(formatter.ConstructEnum) {
		t.Errorf("break mapping wrong: %+v", o)
	}

	// Zero patch keeps the defaults (no forced breaks).
	o, err = (Patch{}).Formatter()
	if err != nil {
		t.Fatalf("Formatter: %v", err)
	}

	for _, c := range formatter.AllConstructs {
		if o.Break.Get(c) {
			t.Errorf("breaks should default to false for %s: %+v", c, o)
		}
	}
}
