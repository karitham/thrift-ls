package formatter

import (
	"github.com/karitham/thrift-ls/options"
)

// FromConfig converts a validated configuration patch to formatter options.
// The formatter owns this translation because the config strings ("field",
// "comma", ...) name formatting concepts only it defines.
func FromConfig(p options.Patch) (Options, error) {
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
		for _, c := range options.AllConstructs {
			if v := p.Separators.Get(c); v != nil {
				if mode, ok := separatorMode(*v); ok {
					o.Separator.Set(c, mode)
				}
			}
		}
	}

	if p.Break != nil {
		for _, c := range options.AllConstructs {
			if v := p.Break.Get(c); v != nil {
				o.Break.Set(c, *v)
			}
		}
	}

	return o, nil
}

// alignMode maps a config value to an align mode. The second result reports
// whether the value is a known align mode.
func alignMode(s string) (AlignMode, bool) {
	switch s {
	case "field":
		return AlignField, true
	case "assign":
		return AlignAssign, true
	case "disable":
		return AlignDisable, true
	default:
		return 0, false
	}
}

// separatorMode maps a config value to a separator mode. The second result
// reports whether the value is a known separator mode.
func separatorMode(s string) (SeparatorMode, bool) {
	switch s {
	case "comma":
		return SeparatorComma, true
	case "semicolon":
		return SeparatorSemicolon, true
	case "none":
		return SeparatorNone, true
	case "preserve":
		return SeparatorPreserve, true
	default:
		return 0, false
	}
}
