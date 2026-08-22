package formatter

import (
	"testing"

	"github.com/karitham/thrift-ls/options"
)

func TestFromConfig(t *testing.T) {
	indent := options.Indent{Value: "  ", Width: 2}
	printWidth := 100
	p := options.Patch{Indent: &indent, PrintWidth: &printWidth}

	o, err := FromConfig(p)
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}

	if o.PrintWidth != 100 || o.Indent != "  " || o.TabWidth != 2 {
		t.Errorf("got %+v", o)
	}

	if o.Align != AlignField || o.Separator.Get(options.ConstructStruct) != SeparatorPreserve {
		t.Errorf("defaults wrong: %+v", o)
	}

	comma := "comma"
	align := "assign"
	separators := options.Separators{Structs: &comma}
	p = options.Patch{Separators: &separators, Align: &align}

	o, err = FromConfig(p)
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}

	if o.Separator.Get(options.ConstructStruct) != SeparatorComma || o.Align != AlignAssign {
		t.Errorf("got %+v", o)
	}
}

// TestFromConfigSeparatorModes maps every config value to the separator
// modes, per construct.
func TestFromConfigSeparatorModes(t *testing.T) {
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
			p := options.Patch{Separators: &options.Separators{
				Structs: &value, Unions: &value, Exceptions: &value,
				Enums: &value, Arguments: &value, Throws: &value,
				Lists: &value, Maps: &value, Sets: &value,
			}}

			o, err := FromConfig(p)
			if err != nil {
				t.Fatalf("FromConfig: %v", err)
			}

			for _, c := range options.AllConstructs {
				if o.Separator.Get(c) != tt.want {
					t.Errorf("value %q: construct %s = %v, want %v", tt.value, c, o.Separator.Get(c), tt.want)
				}
			}
		})
	}

	// The option maps independently per construct.
	semicolon, comma := "semicolon", "comma"
	separators := options.Separators{Structs: &semicolon, Enums: &semicolon, Arguments: &comma, Throws: &comma}
	p := options.Patch{Separators: &separators}

	o, err := FromConfig(p)
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}

	if o.Separator.Get(options.ConstructStruct) != SeparatorSemicolon || o.Separator.Get(options.ConstructArguments) != SeparatorComma {
		t.Errorf("independent mapping failed: %+v", o)
	}
}

// TestFromConfigBreak maps the break group to the formatter options.
func TestFromConfigBreak(t *testing.T) {
	trueVal, falseVal := true, false

	p := options.Patch{Break: &options.Break{Structs: &trueVal, Enums: &falseVal}}

	o, err := FromConfig(p)
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}

	if !o.Break.Get(options.ConstructStruct) || o.Break.Get(options.ConstructEnum) {
		t.Errorf("break mapping wrong: %+v", o)
	}

	// Zero patch keeps the defaults (no forced breaks).
	o, err = FromConfig(options.Patch{})
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}

	for _, c := range options.AllConstructs {
		if o.Break.Get(c) {
			t.Errorf("breaks should default to false for %s: %+v", c, o)
		}
	}
}
