package doc

import (
	"testing"
)

// FuzzPrint checks printer invariants over arbitrary documents built from a
// small bytecode grammar:
//
//   - printing never panics or loops forever, for any document shape
//   - Print only errors on structurally invalid documents (never for the
//     generated ones)
//   - printing the same document bytes twice is deterministic
//
// The generated documents exercise every IR node, nested arbitrarily deep,
// including mutually-recursive shapes (ConditionalGroup states contain
// groups) and documents with no group at all.
func FuzzPrint(f *testing.F) {
	seeds := [][]byte{
		{},
		{opText, 3, 'a', 'b', 'c'},
		{opGroup, opText, 3, 'a', 'b', 'c'},
		{opGroup, opConcat, 3, opText, 1, 'a', opLine, opText, 1, 'b'},
		{opGroup, opText, 6, 'a', 'a', 'a', 'a', opLine, opText, 1, 'b'},
		{opConcat, 3, opText, 1, 'a', opLineSuffix, opText, 4, ' ', '/', '/', 'c', opLine},
		{opConditional, 3, opConcat, 3, opText, 1, 'a', opLine, opText, 1, 'b', opConcat, 2, opText, 1, 'a', opHardLine},
		{opGroup, opText, 6, 'a', 'b', 'c', 'd', opHardLine, opText, 1, 'e'},
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, program []byte) {
		build := func() Doc {
			d, ok := buildDoc(program, 0)
			if !ok {
				t.Skip("program exhausted")
			}

			return d
		}

		opts := Options{PrintWidth: 1 + len(program)%80, Indent: "  ", TabWidth: 2, NewLine: "\n"}

		first, err := Print(build(), opts)
		if err != nil {
			t.Fatalf("Print: %v", err)
		}

		second, err := Print(build(), opts)
		if err != nil {
			t.Fatalf("Print (second): %v", err)
		}

		if first != second {
			t.Fatalf("Print is not deterministic:\n%q\n%q", first, second)
		}
	})
}

// Bytecode operations for the doc generator.
const (
	opText byte = iota
	opLine
	opSoftLine
	opHardLine
	opGroup
	opGroupBreak
	opConcat
	opIndent
	opAlign
	opIfBreak
	opLineSuffix
	opConditional
	opTrim
	opBreakParent
	opLineSuffixBoundary
)

const maxDepth = 6

// textChars map program bytes to characters spanning the width classes:
// ASCII, wide (CJK), zero-width (combining), and control.
var textChars = []rune{'a', 'b', ' ', 'x', '日', '本', '😀', '\u0301', '\t', 'e', '0'}

// buildDoc builds a document from the bytecode program, returning the doc
// and whether the program was fully consumed.
func buildDoc(program []byte, depth int) (Doc, bool) {
	var a Arena

	return buildDocArena(&a, program, depth)
}

// buildDocArena is buildDoc against an arena: it exists so fuzz seeds and
// regression corpus entries (which predate the arena-only API) keep
// building docs by value where convenient.
func buildDocArena(a *Arena, program []byte, depth int) (Doc, bool) {
	if depth > maxDepth || len(program) == 0 {
		return a.Concat(), false
	}

	switch op := program[0]; op {
	case opText:
		n := int(program[1%len(program)]) % 8

		text := make([]rune, 0, n)
		for i := range n {
			text = append(text, textChars[int(program[(2+i)%len(program)])%len(textChars)])
		}

		return a.Text(string(text)), true

	case opLine, opSoftLine, opHardLine:
		switch op {
		case opLine:
			return Line, true
		case opSoftLine:
			return SoftLine, true
		default:
			return HardLine, true
		}

	case opGroup, opGroupBreak:
		inner, ok := buildDocArena(a, program[1:], depth+1)
		if !ok {
			return a.Concat(), false
		}

		if op == opGroupBreak {
			return a.GroupBreak(inner), true
		}

		return a.Group(inner), true

	case opConcat:
		n := int(program[1%len(program)]) % 5
		parts := make([]Doc, 0, n)

		offset := 2
		for range n {
			if offset >= len(program) {
				return a.Concat(parts...), true
			}

			part, ok := buildDocArena(a, program[offset:], depth+1)
			if !ok {
				return a.Concat(parts...), true
			}

			parts = append(parts, part)
			offset++
		}

		return a.Concat(parts...), true

	case opIndent:
		inner, ok := buildDocArena(a, program[1:], depth+1)
		if !ok {
			return a.Concat(), false
		}

		return a.Indent(inner), true

	case opAlign:
		inner, ok := buildDocArena(a, program[1:], depth+1)
		if !ok {
			return a.Concat(), false
		}

		return a.Align(int(program[1%len(program)])%5, inner), true

	case opIfBreak:
		brk, ok1 := buildDocArena(a, program[1:], depth+1)

		flat, ok2 := buildDocArena(a, program[2%len(program):], depth+1)
		if !ok1 || !ok2 {
			return a.Concat(), false
		}

		return a.IfBreak(brk, flat), true

	case opLineSuffix:
		inner, ok := buildDocArena(a, program[1:], depth+1)
		if !ok {
			return a.Concat(), false
		}

		return a.LineSuffix(inner), true

	case opConditional:
		first, ok := buildDocArena(a, program[1:], depth+1)
		if !ok {
			return a.Concat(), false
		}

		second, ok := buildDocArena(a, program[2%len(program):], depth+1)
		if !ok {
			return a.Concat(), false
		}

		return a.ConditionalGroup(0, first, second, a.GroupBreak(first)), true

	case opTrim:
		return TrimDoc, true

	case opBreakParent:
		return BreakParent, true

	case opLineSuffixBoundary:
		return LineSuffixBoundary, true

	default:
		return a.Concat(), false
	}
}
