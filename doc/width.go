package doc

import (
	"unicode"

	"golang.org/x/text/width"
)

// stringWidth returns the display width of s in monospace columns: wide
// characters (East Asian W/F) count as 2, zero-width characters (combining
// marks, variation selectors) and control characters count as 0.
//
// Width classification uses the stdlib unicode package for categories and
// properties, and golang.org/x/text/width for the East Asian Width classes,
// the same data Prettier measures with. Ambiguous-width characters count as
// narrow, like Prettier.
func stringWidth(s string) int {
	// Fast path: pure ASCII without control characters has width equal to
	// its byte length.
	ascii := true

	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 || s[i] <= 0x1f || s[i] == 0x7f {
			ascii = false

			break
		}
	}

	if ascii {
		return len(s)
	}

	width := 0

	for _, r := range s {
		switch {
		case unicode.Is(unicode.Cc, r):
			// Control characters take no space.
		case isZeroWidth(r):
			// Combining marks and variation selectors.
		case isWide(r):
			width += 2
		default:
			width++
		}
	}

	return width
}

// isZeroWidth reports whether r renders zero columns wide: combining marks
// (Nonspacing_Mark and Enclosing_Mark, matching Prettier, which excludes
// spacing marks) and variation selectors.
func isZeroWidth(r rune) bool {
	return unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Variation_Selector, r)
}

// isWide reports whether r renders two columns wide per the East Asian Width
// classes W and F (golang.org/x/text/width).
func isWide(r rune) bool {
	switch width.LookupRune(r).Kind() {
	case width.EastAsianWide, width.EastAsianFullwidth:
		return true
	}

	return false
}
