// Package options is the configuration layer of thrift-ls. It owns a
// partial-options model (Patch): every field is optional so configuration
// sources can be layered — defaults, a JSON config file, CLI flags, and LSP
// workspace settings — each overriding the previous.
//
// The config file is thriftls.json, discovered by walking up from the file
// being formatted, like Biome's config discovery. THRIFTLS_CONFIG overrides
// the search with an explicit path.
package options

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/karitham/thrift-ls/formatter"
)

// ConfigFileName is the JSON config file name.
const ConfigFileName = "thriftls.json"

// Separators configures trailing separators for the two field contexts.
type Separators struct {
	// Fields controls separators after struct/union/exception fields and
	// enum values.
	Fields *string `json:"fields"`
	// Functions controls separators after service arguments and throws
	// entries.
	Functions *string `json:"functions"`
}

// Break configures layouts that are forced multiline.
type Break struct {
	// Structs forces struct, union, and exception bodies multiline.
	Structs *bool `json:"structs"`
	// Enums forces enum bodies multiline.
	Enums *bool `json:"enums"`
}

// Patch is a partial set of options; nil fields are unset.
type Patch struct {
	PrintWidth   *int        `json:"printWidth"`
	Indent       *Indent     `json:"indent"`
	TabWidth     *int        `json:"tabWidth"`
	Align        *string     `json:"align"`
	Separators   *Separators `json:"separators"`
	Break        *Break      `json:"break"`
	IncludePaths *[]string   `json:"includePaths"`
	LogLevel     *int        `json:"logLevel"`
}

// Apply overlays p onto base: every set field of p replaces the
// corresponding field of base.
func (p Patch) Apply(base Patch) Patch {
	out := base
	if p.PrintWidth != nil {
		out.PrintWidth = p.PrintWidth
	}
	if p.Indent != nil {
		out.Indent = p.Indent
	}
	if p.TabWidth != nil {
		out.TabWidth = p.TabWidth
	}
	if p.Align != nil {
		out.Align = p.Align
	}
	if p.Separators != nil {
		if out.Separators == nil {
			out.Separators = &Separators{}
		}
		if p.Separators.Fields != nil {
			out.Separators.Fields = p.Separators.Fields
		}
		if p.Separators.Functions != nil {
			out.Separators.Functions = p.Separators.Functions
		}
	}
	if p.Break != nil {
		if out.Break == nil {
			out.Break = &Break{}
		}
		if p.Break.Structs != nil {
			out.Break.Structs = p.Break.Structs
		}
		if p.Break.Enums != nil {
			out.Break.Enums = p.Break.Enums
		}
	}
	if p.IncludePaths != nil {
		out.IncludePaths = p.IncludePaths
	}
	if p.LogLevel != nil {
		out.LogLevel = p.LogLevel
	}
	return out
}

// Default returns the default options as a fully-set patch.
func Default() Patch {
	printWidth := 80
	indent := Indent{Value: "    ", Width: 4}
	tabWidth := 4
	align := "field"
	separators := Separators{Fields: new("disable"), Functions: new("disable")}
	return Patch{
		PrintWidth: &printWidth,
		Indent:     &indent,
		TabWidth:   &tabWidth,
		Align:      &align,
		Separators: &separators,
	}
}

// Validate checks every set field for validity.
func (p Patch) Validate() error {
	if p.PrintWidth != nil && *p.PrintWidth <= 0 {
		return errors.New("printWidth must be positive")
	}
	if p.TabWidth != nil && *p.TabWidth <= 0 {
		return errors.New("tabWidth must be positive")
	}
	if p.Align != nil && !oneOf(*p.Align, "field", "assign", "disable") {
		return fmt.Errorf("align must be one of \"field\", \"assign\", \"disable\", got %q", *p.Align)
	}
	if p.Separators != nil {
		for _, v := range []struct {
			name  string
			value *string
		}{
			{"separators.fields", p.Separators.Fields},
			{"separators.functions", p.Separators.Functions},
		} {
			if v.value != nil && !oneOf(*v.value, "add", "remove", "semicolon", "disable", "preserve") {
				return fmt.Errorf("%s must be one of \"add\", \"remove\", \"semicolon\", \"disable\" (keep as written), got %q", v.name, *v.value)
			}
		}
	}
	if p.Indent != nil {
		if p.Indent.Width <= 0 || !isWhitespaceOnly(p.Indent.Value) {
			return errors.New("indent must be a string of spaces or tabs")
		}
	}
	return nil
}

func oneOf(s string, options ...string) bool {
	return slices.Contains(options, s)
}

// Formatter converts the patch to formatter options, validating first.
func (p Patch) Formatter() (formatter.Options, error) {
	if err := p.Validate(); err != nil {
		return formatter.Options{}, err
	}
	o := formatter.DefaultOptions()
	if p.PrintWidth != nil {
		o.PrintWidth = *p.PrintWidth
	}
	if p.Indent != nil {
		o.Indent = p.Indent.Value
		o.TabWidth = p.Indent.Width
	}
	if p.TabWidth != nil {
		o.TabWidth = *p.TabWidth
	}
	if p.Align != nil {
		switch *p.Align {
		case "field":
			o.Align = formatter.AlignField
		case "assign":
			o.Align = formatter.AlignAssign
		case "disable":
			o.Align = formatter.AlignDisable
		}
	}
	if p.Separators != nil {
		if p.Separators.Fields != nil {
			o.FieldSeparator = separatorMode(*p.Separators.Fields)
		}
		if p.Separators.Functions != nil {
			o.FunctionSeparator = separatorMode(*p.Separators.Functions)
		}
	}
	if p.Break != nil {
		if p.Break.Structs != nil {
			o.BreakStructs = *p.Break.Structs
		}
		if p.Break.Enums != nil {
			o.BreakEnums = *p.Break.Enums
		}
	}
	return o, nil
}

// separatorMode maps a config value to a formatter separator mode. The
// value is validated before this is called.
func separatorMode(s string) formatter.SeparatorMode {
	switch s {
	case "add":
		return formatter.SeparatorComma
	case "remove":
		return formatter.SeparatorNone
	case "semicolon":
		return formatter.SeparatorSemicolon
	default: // "disable", "preserve"
		return formatter.SeparatorPreserve
	}
}

// Indent is a resolved indentation: the string emitted for one level and
// its display width. It is set from a config value that may be a literal
// string of spaces or tabs, a number of spaces, or a legacy spec like
// "2spaces" or "1tab".
type Indent struct {
	Value string // the indentation string, spaces or tabs
	Width int    // display width of one level
}

// UnmarshalJSON accepts a number (spaces), a literal string of spaces or
// tabs, or a legacy spec string.
func (i *Indent) UnmarshalJSON(data []byte) error {
	var n int
	if err := json.Unmarshal(data, &n); err == nil {
		ind, err := ParseIndentValue(strconv.Itoa(n))
		if err != nil {
			return err
		}
		*i = ind
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return errors.New("indent must be a string of spaces or tabs, a number, or a legacy spec like \"2spaces\"")
	}
	ind, err := ParseIndentValue(s)
	if err != nil {
		return err
	}
	*i = ind
	return nil
}

// ParseIndentValue resolves a friendly indent spec:
//
//	"  "   literal spaces, used as written
//	"\t"   literal tabs, used as written
//	"8"    a number of spaces
//	"2spaces", "1tab", "tab"   legacy specs, kept as aliases
//
// An empty spec yields the default of four spaces.
func ParseIndentValue(s string) (Indent, error) {
	if s == "" {
		return Indent{Value: "    ", Width: 4}, nil
	}
	if n, err := strconv.Atoi(s); err == nil {
		if n <= 0 {
			return Indent{}, errors.New("indent must be a positive number of spaces")
		}
		return Indent{Value: strings.Repeat(" ", n), Width: n}, nil
	}
	if isWhitespaceOnly(s) {
		spaces := strings.Count(s, " ")
		tabs := strings.Count(s, "\t")
		if spaces > 0 && tabs > 0 {
			return Indent{}, fmt.Errorf("indent %q mixes spaces and tabs", s)
		}
		if tabs > 0 {
			return Indent{Value: s, Width: tabs * 4}, nil
		}
		return Indent{Value: s, Width: spaces}, nil
	}
	return ParseLegacyIndent(s)
}

func isWhitespaceOnly(s string) bool {
	for _, r := range s {
		if r != ' ' && r != '\t' {
			return false
		}
	}
	return true
}

// ParseLegacyIndent parses the legacy indent specs ("4spaces", "1tab",
// "2tabs", "tab"), kept for compatibility.
func ParseLegacyIndent(s string) (Indent, error) {
	lower := strings.ToLower(s)
	num := 1
	unit := ""
	for _, suffix := range []string{"spaces", "space", "tabs", "tab"} {
		if strings.HasSuffix(lower, suffix) {
			unit = suffix
			prefix := strings.TrimSuffix(lower, suffix)
			if prefix != "" {
				n, err := strconv.Atoi(prefix)
				if err != nil || n <= 0 {
					return Indent{}, fmt.Errorf("invalid indent %q: use a literal like \"  \", a number like 8, or a legacy spec like \"2spaces\"", s)
				}
				num = n
			}
			break
		}
	}
	if unit == "" {
		return Indent{}, fmt.Errorf("invalid indent %q: use a literal like \"  \", a number like 8, or a legacy spec like \"2spaces\"", s)
	}
	if strings.HasPrefix(unit, "tab") {
		return Indent{Value: strings.Repeat("\t", num), Width: num * 4}, nil
	}
	return Indent{Value: strings.Repeat(" ", num), Width: num}, nil
}

// Load reads and parses a config file. Unknown keys are rejected so that
// typos and stale settings (e.g. the removed overrides feature) fail loudly.
func Load(path string) (*Patch, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Patch
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("options: %s: %w", path, err)
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("options: %s: %w", path, err)
	}
	// Include paths are relative to the config file, not the process CWD,
	// so resolution works the same for the CLI and the LSP no matter where
	// the server is launched from.
	if p.IncludePaths != nil {
		abs := make([]string, 0, len(*p.IncludePaths))
		for _, ip := range *p.IncludePaths {
			if filepath.IsAbs(ip) {
				abs = append(abs, ip)
			} else {
				abs = append(abs, filepath.Join(filepath.Dir(path), ip))
			}
		}
		p.IncludePaths = &abs
	}
	return &p, nil
}

// FindConfig returns the config file path for dir: THRIFTLS_CONFIG when set,
// otherwise the nearest thriftls.json walking up from dir. It returns an
// empty path when no config exists.
func FindConfig(dir string) (string, error) {
	if path := os.Getenv("THRIFTLS_CONFIG"); path != "" {
		return path, nil
	}
	for d := dir; ; d = filepath.Dir(d) {
		path := filepath.Join(d, ConfigFileName)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		if d == filepath.Dir(d) {
			return "", nil
		}
	}
}

// Effective returns the effective options for a file: defaults with the
// config patch applied. cfg may be nil.
func Effective(cfg *Patch) Patch {
	p := Default()
	if cfg != nil {
		p = cfg.Apply(p)
	}
	return p
}
