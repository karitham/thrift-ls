package formatter

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Construct identifies one formatting construct that per-construct options
// apply to: the container bodies (structs, unions, exceptions, enums,
// arguments, throws) and the collection types (lists, maps, sets).
type Construct int

const (
	ConstructStruct Construct = iota
	ConstructUnion
	ConstructException
	ConstructEnum
	ConstructArguments
	ConstructThrows
	ConstructList
	ConstructMap
	ConstructSet
)

// PerConstruct holds one option value per construct. The JSON tags make the
// per-construct option maps config-compatible ("structs", "arguments", ...),
// so the config layer and the CLI share this single source of truth.
type PerConstruct[T any] struct {
	Structs    T `json:"structs"`
	Unions     T `json:"unions"`
	Exceptions T `json:"exceptions"`
	Enums      T `json:"enums"`
	Arguments  T `json:"arguments"`
	Throws     T `json:"throws"`
	Lists      T `json:"lists"`
	Maps       T `json:"maps"`
	Sets       T `json:"sets"`
}

// Get returns the value for the construct.
func (p PerConstruct[T]) Get(c Construct) T {
	switch c {
	case ConstructUnion:
		return p.Unions
	case ConstructException:
		return p.Exceptions
	case ConstructEnum:
		return p.Enums
	case ConstructArguments:
		return p.Arguments
	case ConstructThrows:
		return p.Throws
	case ConstructList:
		return p.Lists
	case ConstructMap:
		return p.Maps
	case ConstructSet:
		return p.Sets
	}

	return p.Structs
}

// Set assigns the value for the construct.
func (p *PerConstruct[T]) Set(c Construct, v T) {
	switch c {
	case ConstructUnion:
		p.Unions = v
	case ConstructException:
		p.Exceptions = v
	case ConstructEnum:
		p.Enums = v
	case ConstructArguments:
		p.Arguments = v
	case ConstructThrows:
		p.Throws = v
	case ConstructList:
		p.Lists = v
	case ConstructMap:
		p.Maps = v
	case ConstructSet:
		p.Sets = v
	default:
		p.Structs = v
	}
}

// AllConstructs lists every construct, in config order.
var AllConstructs = []Construct{
	ConstructStruct, ConstructUnion, ConstructException,
	ConstructEnum, ConstructArguments, ConstructThrows,
	ConstructList, ConstructMap, ConstructSet,
}

// String returns the config key of the construct.
func (c Construct) String() string {
	switch c {
	case ConstructUnion:
		return "unions"
	case ConstructException:
		return "exceptions"
	case ConstructEnum:
		return "enums"
	case ConstructArguments:
		return "arguments"
	case ConstructThrows:
		return "throws"
	case ConstructList:
		return "lists"
	case ConstructMap:
		return "maps"
	case ConstructSet:
		return "sets"
	}

	return "structs"
}

// Separators configures trailing separators per construct. A nil value is
// unset.
type Separators = PerConstruct[*string]

// Break configures layouts that are forced multiline per construct. A nil
// value is unset.
type Break = PerConstruct[*bool]

// FormatPatch is a partial formatting configuration; nil fields are unset,
// so layered sources (defaults, config file, CLI flags, workspace settings)
// override each other field by field.
type FormatPatch struct {
	PrintWidth *int        `json:"printWidth"`
	Indent     *Indent     `json:"indent"`
	TabWidth   *int        `json:"tabWidth"`
	Align      *string     `json:"align"`
	Separators *Separators `json:"separators"`
	Break      *Break      `json:"break"`
}

// Apply overlays p onto base: every set field of p replaces the
// corresponding field of base.
func (p FormatPatch) Apply(base FormatPatch) FormatPatch {
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

// DefaultFormatPatch returns the default formatting configuration as a
// fully-set patch.
func DefaultFormatPatch() FormatPatch {
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

	return FormatPatch{
		PrintWidth: &printWidth,
		Indent:     &indent,
		TabWidth:   &tabWidth,
		Align:      &align,
		Separators: &separators,
	}
}

// Validate checks every set field for validity.
func (p FormatPatch) Validate() error {
	if p.PrintWidth != nil && *p.PrintWidth <= 0 {
		return errors.New("printWidth must be positive")
	}

	if p.TabWidth != nil && *p.TabWidth <= 0 {
		return errors.New("tabWidth must be positive")
	}

	if p.Align != nil {
		if _, ok := alignMode(*p.Align); !ok {
			return fmt.Errorf("align must be one of \"field\", \"assign\", \"disable\", got %q", *p.Align)
		}
	}

	if p.Separators != nil {
		for _, c := range AllConstructs {
			if v := p.Separators.Get(c); v != nil {
				if _, ok := separatorMode(*v); !ok {
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

// Options converts the patch to formatter options, validating first.
func (p FormatPatch) Options() (Options, error) {
	if err := p.Validate(); err != nil {
		return Options{}, err
	}

	o := DefaultOptions()
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
		if mode, ok := alignMode(*p.Align); ok {
			o.Align = mode
		}
	}

	if p.Separators != nil {
		for _, c := range AllConstructs {
			if v := p.Separators.Get(c); v != nil {
				if mode, ok := separatorMode(*v); ok {
					o.Separator.Set(c, mode)
				}
			}
		}
	}

	if p.Break != nil {
		for _, c := range AllConstructs {
			if v := p.Break.Get(c); v != nil {
				o.Break.Set(c, *v)
			}
		}
	}

	return o, nil
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
