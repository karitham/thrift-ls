package formatter

import (
	"encoding/json"
	"testing"
)

func TestFormatPatchOptions(t *testing.T) {
	indent := Indent{Value: "  ", Width: 2}
	printWidth := 100
	p := FormatPatch{Indent: &indent, PrintWidth: &printWidth}

	o, err := p.Options()
	if err != nil {
		t.Fatalf("Options: %v", err)
	}

	if o.PrintWidth != 100 || o.Indent != "  " || o.TabWidth != 2 {
		t.Errorf("got %+v", o)
	}

	if o.Align != AlignField || o.Separator.Get(ConstructStruct) != SeparatorPreserve {
		t.Errorf("defaults wrong: %+v", o)
	}

	comma := "comma"
	align := "assign"
	separators := Separators{Structs: &comma}
	p = FormatPatch{Separators: &separators, Align: &align}

	o, err = p.Options()
	if err != nil {
		t.Fatalf("Options: %v", err)
	}

	if o.Separator.Get(ConstructStruct) != SeparatorComma || o.Align != AlignAssign {
		t.Errorf("got %+v", o)
	}
}

func TestFormatPatchApplyDoesNotMutateBase(t *testing.T) {
	baseBreak := true
	overlayBreak := false
	base := FormatPatch{Break: &Break{Structs: &baseBreak}}
	overlay := FormatPatch{Break: &Break{Structs: &overlayBreak}}

	result := overlay.Apply(base)

	if *result.Break.Structs != false {
		t.Fatalf("result break.structs = %v, want false", *result.Break.Structs)
	}

	if *base.Break.Structs != true {
		t.Fatalf("base break.structs = %v, want true", *base.Break.Structs)
	}
}

// TestFormatPatchSeparatorModes maps every config value to the separator
// modes, per construct.
func TestFormatPatchSeparatorModes(t *testing.T) {
	tests := []struct {
		value string
		want  SeparatorMode
	}{
		{"comma", SeparatorComma},
		{"none", SeparatorNone},
		{"semicolon", SeparatorSemicolon},
		{"preserve", SeparatorPreserve},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			value := tt.value
			p := FormatPatch{Separators: &Separators{
				Structs: &value, Unions: &value, Exceptions: &value,
				Enums: &value, Arguments: &value, Throws: &value,
				Lists: &value, Maps: &value, Sets: &value,
			}}

			o, err := p.Options()
			if err != nil {
				t.Fatalf("Options: %v", err)
			}

			for _, c := range AllConstructs {
				if o.Separator.Get(c) != tt.want {
					t.Errorf("value %q: construct %s = %v, want %v", tt.value, c, o.Separator.Get(c), tt.want)
				}
			}
		})
	}

	// The option maps independently per construct.
	semicolon, comma := "semicolon", "comma"
	separators := Separators{Structs: &semicolon, Enums: &semicolon, Arguments: &comma, Throws: &comma}
	p := FormatPatch{Separators: &separators}

	o, err := p.Options()
	if err != nil {
		t.Fatalf("Options: %v", err)
	}

	if o.Separator.Get(ConstructStruct) != SeparatorSemicolon || o.Separator.Get(ConstructArguments) != SeparatorComma {
		t.Errorf("independent mapping failed: %+v", o)
	}
}

// TestFormatPatchBreak maps the break group to the formatter options.
func TestFormatPatchBreak(t *testing.T) {
	trueVal, falseVal := true, false

	p := FormatPatch{Break: &Break{Structs: &trueVal, Enums: &falseVal}}

	o, err := p.Options()
	if err != nil {
		t.Fatalf("Options: %v", err)
	}

	if !o.Break.Get(ConstructStruct) || o.Break.Get(ConstructEnum) {
		t.Errorf("break mapping wrong: %+v", o)
	}

	// Zero patch keeps the defaults (no forced breaks).
	o, err = (FormatPatch{}).Options()
	if err != nil {
		t.Fatalf("Options: %v", err)
	}

	for _, c := range AllConstructs {
		if o.Break.Get(c) {
			t.Errorf("breaks should default to false for %s: %+v", c, o)
		}
	}
}

func TestFormatPatchValidate(t *testing.T) {
	intPtr := func(n int) *int { return &n }
	strPtr := func(s string) *string { return &s }

	tests := []struct {
		name    string
		patch   FormatPatch
		wantErr bool
	}{
		{"default is valid", DefaultFormatPatch(), false},
		{"bad printWidth", FormatPatch{PrintWidth: intPtr(0)}, true},
		{"bad tabWidth", FormatPatch{TabWidth: intPtr(-1)}, true},
		{"zero tabWidth", FormatPatch{TabWidth: intPtr(0)}, true},
		{"bad align", FormatPatch{Align: strPtr("sideways")}, true},
		{"bad separator value", FormatPatch{Separators: &Separators{Structs: strPtr("maybe")}}, true},
		{"preserve alias", FormatPatch{Separators: &Separators{Structs: strPtr("preserve")}}, false},
		{"bad indent value", FormatPatch{Indent: &Indent{Value: "x", Width: 1}}, true},
		{"zero indent width", FormatPatch{Indent: &Indent{Value: "  ", Width: 0}}, true},
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
		name    string
		json    string
		want    Indent
		wantErr bool
	}{
		{"string spaces", `"  "`, Indent{"  ", 2}, false},
		{"string tab", `"\t"`, Indent{"\t", 4}, false},
		{"non-string", `123`, Indent{}, true},
		{"invalid literal", `"banana"`, Indent{}, true},
		{"mixed whitespace", `" \t"`, Indent{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var i Indent
			if err := json.Unmarshal([]byte(tt.json), &i); err != nil {
				if !tt.wantErr {
					t.Fatalf("unmarshal: %v", err)
				}

				return
			}
			if tt.wantErr {
				t.Fatal("expected error, got none")
			}

			if i != tt.want {
				t.Errorf("got %+v, want %+v", i, tt.want)
			}
		})
	}
}
