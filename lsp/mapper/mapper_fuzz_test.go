package mapper

import (
	"testing"
	"unicode/utf8"
)

// FuzzOffsetRoundTrip checks that byte offsets round-trip through
// OffsetToLSPPosition and back to the same offset, for offsets aligned to
// rune boundaries (LSP positions are UTF-16 code-unit aligned, so byte
// offsets inside a multi-byte rune have no representable position).
func FuzzOffsetRoundTrip(f *testing.F) {
	seeds := []string{
		"struct A {\n1: string a\n}",
		"struct A { 1: string s } // café ☕",
		"中文注释\nstruct A { 1: string x }",
		"emoji 🎉 in comment\nstruct B {}",
		"",
		"\n",
		"a\r\nb\r\nc",
	}
	for _, s := range seeds {
		f.Add([]byte(s), 0)
	}

	f.Fuzz(func(t *testing.T, content []byte, offset int) {
		limit := len(content) + 1

		offset = offset % limit
		if offset < 0 {
			offset += limit
		}

		if offset < len(content) && !utf8.RuneStart(content[offset]) {
			// Byte offset inside a multi-byte rune: not representable.
			return
		}

		m := NewMapper(content)

		pos, err := m.OffsetToLSPPosition(offset)
		if err != nil {
			t.Fatalf("OffsetToLSPPosition(%d): %v", offset, err)
		}

		back, err := m.LSPPosToParserPosition(pos)
		if err != nil {
			t.Fatalf("LSPPosToParserPosition(%+v): %v (content %q)", pos, err, content)
		}

		if back.Offset != offset {
			t.Fatalf("round trip mismatch: %d -> %+v -> %d (content %q)", offset, pos, back.Offset, content)
		}
	})
}
