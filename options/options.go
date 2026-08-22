// Package options is the configuration layer of thrift-ls. It owns a
// partial-options model (Patch): every field is optional so configuration
// sources can be layered — defaults, a JSON config file, CLI flags, and LSP
// workspace settings — each overriding the previous.
//
// The config file is thrift-ls.json, discovered by walking up from the file
// being formatted, like Biome's config discovery. THRIFT_LS_CONFIG overrides
// the search with an explicit path.
package options

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ConfigFileName is the JSON config file name.
const ConfigFileName = "thrift-ls.json"

// Separators configures trailing separators per construct. A nil value is
// unset. It is an alias of the per-construct container, so adding a
// construct adds the config key, CLI flags, and validation in one place.
type Separators = PerConstruct[*string]

// Break configures layouts that are forced multiline per construct. A nil
// value is unset.
type Break = PerConstruct[*bool]

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

	out.Separators = overlayPerConstruct(out.Separators, p.Separators)
	out.Break = overlayPerConstruct(out.Break, p.Break)

	if p.IncludePaths != nil {
		out.IncludePaths = p.IncludePaths
	}

	if p.LogLevel != nil {
		out.LogLevel = p.LogLevel
	}

	return out
}

// overlayPerConstruct copies the set fields of src onto dst, creating dst
// when it is nil.
func overlayPerConstruct[T *E, E any](dst, src *PerConstruct[T]) *PerConstruct[T] {
	if src == nil {
		return dst
	}

	if dst == nil {
		dst = &PerConstruct[T]{}
	}

	for _, c := range AllConstructs {
		if v := src.Get(c); v != nil {
			dst.Set(c, v)
		}
	}

	return dst
}

// Default returns the default options as a fully-set patch.
func Default() Patch {
	printWidth := 80
	indent := Indent{Value: "    ", Width: 4}
	tabWidth := 4
	align := "field"
	separators := Separators{
		Structs:    new("preserve"),
		Unions:     new("preserve"),
		Exceptions: new("preserve"),
		Enums:      new("preserve"),
		Arguments:  new("preserve"),
		Throws:     new("preserve"),
	}

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

	if p.Align != nil {
		if !validAlign(*p.Align) {
			return fmt.Errorf("align must be one of \"field\", \"assign\", \"disable\", got %q", *p.Align)
		}
	}

	if p.Separators != nil {
		for _, c := range AllConstructs {
			if v := p.Separators.Get(c); v != nil {
				if !validSeparator(*v) {
					return fmt.Errorf("separators.%s must be one of \"comma\", \"semicolon\", \"none\", \"preserve\" (keep as written), got %q", c, *v)
				}
			}
		}
	}

	if p.Indent != nil && (p.Indent.Width <= 0 || !isWhitespaceOnly(p.Indent.Value)) {
		return errors.New("indent must be a string of spaces or tabs")
	}

	return nil
}

// validAlign reports whether s is a known align config value.
func validAlign(s string) bool {
	switch s {
	case "field", "assign", "disable":
		return true
	}

	return false
}

// validSeparator reports whether s is a known separator config value.
func validSeparator(s string) bool {
	switch s {
	case "comma", "semicolon", "none", "preserve":
		return true
	}

	return false
}

// Indent is a resolved indentation: the string emitted for one level and
// its display width. It is set from a literal string of spaces or tabs.
type Indent struct {
	Value string // the indentation string, spaces or tabs
	Width int    // display width of one level
}

// UnmarshalJSON accepts a literal string of spaces or tabs.
func (i *Indent) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return errors.New("indent must be a string of spaces or tabs")
	}

	ind, err := ParseIndentValue(s)
	if err != nil {
		return err
	}

	*i = ind

	return nil
}

// ParseIndentValue resolves a literal indent string:
//
//	"  "   literal spaces, used as written
//	"\t"   literal tabs, used as written
//
// An empty spec yields the default of four spaces.
func ParseIndentValue(s string) (Indent, error) {
	if s == "" {
		return Indent{Value: "    ", Width: 4}, nil
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

	return Indent{}, errors.New("indent must be a string of spaces or tabs")
}

func isWhitespaceOnly(s string) bool {
	for _, r := range s {
		if r != ' ' && r != '\t' {
			return false
		}
	}

	return true
}

// Parse reads and parses a config document. Unknown keys are rejected so
// that typos and stale settings (e.g. the removed overrides feature) fail
// loudly. Include paths in the document are left as written; Load resolves
// them against the config file's directory.
func Parse(data []byte) (*Patch, error) {
	var p Patch

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&p); err != nil {
		return nil, err
	}

	if err := p.Validate(); err != nil {
		return nil, err
	}

	return &p, nil
}

// Load reads and parses a config file. Unknown keys are rejected so that
// typos and stale settings (e.g. the removed overrides feature) fail loudly.
func Load(path string) (*Patch, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	p, err := Parse(data)
	if err != nil {
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

	return p, nil
}

// FindConfig returns the config file path for dir: THRIFT_LS_CONFIG when set,
// otherwise the nearest thrift-ls.json walking up from dir. It returns an
// empty path when no config exists.
func FindConfig(dir string) (string, error) {
	if path := os.Getenv("THRIFT_LS_CONFIG"); path != "" {
		return path, nil
	}

	for d := dir; ; d = filepath.Dir(d) {
		path := filepath.Join(d, ConfigFileName)
		_, err := os.Stat(path)
		if err == nil {
			return path, nil
		}

		if !os.IsNotExist(err) {
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
