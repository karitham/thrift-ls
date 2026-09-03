package mapper

import (
	"bytes"
	"fmt"
	"sort"
	"sync"
	"unicode/utf8"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/syntax"
)

type Mapper struct {
	content []byte

	lineInit  sync.Once
	lineStart []int // line start 0-based byte offset. lsp: 0-based, parser: 1-based
	nonASCII  bool
}

// NewMapper returns a Mapper for the given document content.
func NewMapper(content []byte) *Mapper {
	return &Mapper{
		content: content,
	}
}

func (m *Mapper) initLineStart() {
	m.lineInit.Do(func() {
		nlines := bytes.Count(m.content, []byte("\n"))

		m.lineStart = make([]int, 1, nlines+1) // initially []int{0}
		for offset, b := range m.content {
			if b == '\n' {
				m.lineStart = append(m.lineStart, offset+1)
			}

			if b >= utf8.RuneSelf {
				m.nonASCII = true
			}
		}
	})
}

// GetLSPEndPosition returns the position immediately after the last
// character of the document: the last line (0-based) at the UTF-16 length
// of its content. A document ending with a newline has an empty last line.
func (m *Mapper) GetLSPEndPosition() protocol.Position {
	m.initLineStart()
	lastLineStart := m.lineStart[len(m.lineStart)-1]
	lastLine := m.content[lastLineStart:]

	return protocol.Position{
		Line:      uint32(len(m.lineStart) - 1),
		Character: uint32(utf16Count(lastLine)),
	}
}

// OffsetToLSPPosition converts a byte offset in the mapped content to an LSP
// position (0-based line, UTF-16 code-unit column).
func (m *Mapper) OffsetToLSPPosition(offset int) (protocol.Position, error) {
	m.initLineStart()

	if offset < 0 || offset > len(m.content) {
		return protocol.Position{}, fmt.Errorf("invalid offset: %d, total content: %d", offset, len(m.content))
	}

	line := max(sort.Search(len(m.lineStart), func(i int) bool { return m.lineStart[i] > offset })-1, 0)

	return protocol.Position{
		Line:      uint32(line),
		Character: uint32(utf16Count(m.content[m.lineStart[line]:offset])),
	}, nil
}

// LSPPosToParserPosition converts an LSP position (0-based line, UTF-16
// code-unit column) to a parser position (1-based line, rune-based column).
func (m *Mapper) LSPPosToParserPosition(pos protocol.Position) (syntax.Position, error) {
	m.initLineStart()

	line := int(pos.Line) + 1
	if line > len(m.lineStart) {
		return syntax.InvalidPosition, fmt.Errorf("invalid position line, request line: %d, total line: %d", line, len(m.lineStart))
	}

	lineStart := m.lineStart[pos.Line]
	lineEnd := len(m.content)
	if line < len(m.lineStart) {
		lineEnd = m.lineStart[line]
	}

	if !m.nonASCII {
		col := int(pos.Character) + 1

		offset := lineStart + int(pos.Character)
		if offset > len(m.content) {
			return syntax.InvalidPosition, fmt.Errorf("invalid position offset: %d, total content: %d, %s", offset, len(m.content), string(m.content))
		}

		if col > lineEnd-lineStart+1 { // if line length is 0, col is 1 means col is at end of line
			return syntax.InvalidPosition, fmt.Errorf("invalid position column: %d, line length: %d, %s", col, lineEnd-lineStart, string(m.content))
		}

		return syntax.Position{
			Line:   line,
			Col:    col,
			Offset: offset,
		}, nil
	}

	lineBytes := m.content[lineStart:lineEnd]

	utf16Col := 0
	bytesCol := 0

	for len(lineBytes) > 0 && utf16Col < int(pos.Character) {
		if lineBytes[0] < utf8.RuneSelf {
			utf16Col++
			lineBytes = lineBytes[1:]
			bytesCol++

			continue
		}

		r, size := utf8.DecodeRune(lineBytes)

		utf16Col++
		if r >= 0x10000 {
			utf16Col++
		}

		lineBytes = lineBytes[size:]
		bytesCol += size
	}

	runeLen := utf8.RuneCount(m.content[lineStart : lineStart+bytesCol])

	return syntax.Position{
		Line:   line,
		Col:    runeLen + 1,
		Offset: lineStart + bytesCol,
	}, nil
}

func utf16Count(contents []byte) int {
	utf16Len := 0
	for len(contents) > 0 {
		utf16Len++

		r, size := utf8.DecodeRune(contents)
		if r >= 0x10000 {
			utf16Len++
		}

		contents = contents[size:]
	}

	return utf16Len
}
