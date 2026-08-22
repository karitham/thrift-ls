package formatter

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
