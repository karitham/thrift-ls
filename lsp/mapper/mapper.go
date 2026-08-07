package mapper

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"sync"
	"unicode/utf8"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/types"
	"github.com/karitham/thrift-ls/syntax"
)

type Mapper struct {
	fileURI uri.URI
	content []byte

	lineInit  sync.Once
	lineStart []int // line start 0-based byte offset. lsp: 0-based, parser: 1-based
	nonASCII  bool
}

// NewMapper ...
func NewMapper(fileURI uri.URI, content []byte) *Mapper {
	return &Mapper{
		fileURI: fileURI,
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
func (m *Mapper) GetLSPEndPosition() types.Position {
	m.initLineStart()
	lastLineStart := m.lineStart[len(m.lineStart)-1]
	lastLine := m.content[lastLineStart:]

	return types.Position{
		Line:      uint32(len(m.lineStart) - 1),
		Character: uint32(utf16Count(lastLine)),
	}
}

// OffsetToLSPPosition converts a byte offset in the mapped content to an LSP
// position (0-based line, UTF-16 code-unit column).
func (m *Mapper) OffsetToLSPPosition(offset int) (types.Position, error) {
	m.initLineStart()

	if offset < 0 || offset > len(m.content) {
		return types.Position{}, fmt.Errorf("invalid offset: %d, total content: %d", offset, len(m.content))
	}

	line := max(sort.Search(len(m.lineStart), func(i int) bool { return m.lineStart[i] > offset })-1, 0)

	return types.Position{
		Line:      uint32(line),
		Character: uint32(utf16Count(m.content[m.lineStart[line]:offset])),
	}, nil
}

// convert from utf16-based to rune-based position
func (m *Mapper) LSPPosToParserPosition(pos types.Position) (syntax.Position, error) {
	m.initLineStart()

	line := int(pos.Line) + 1
	if line > len(m.lineStart) {
		return syntax.InvalidPosition, fmt.Errorf("invalid position line, request line: %d, total line: %d", line, len(m.lineStart))
	}

	if !m.nonASCII {
		col := int(pos.Character) + 1

		offset := m.lineStart[pos.Line] + int(pos.Character)
		if offset > len(m.content) {
			return syntax.InvalidPosition, fmt.Errorf("invalid position offset: %d, total content: %d, %s", offset, len(m.content), string(m.content))
		}

		var lineLength int
		if int(pos.Line+1) >= len(m.lineStart) {
			lineLength = len(m.content) - m.lineStart[pos.Line]
		} else {
			lineLength = m.lineStart[pos.Line+1] - m.lineStart[pos.Line]
		}

		if col > lineLength+1 { // if line length is 0, col is 1 means col is at end of line
			return syntax.InvalidPosition, fmt.Errorf("invalid position column: %d, line length: %d, %s", col, lineLength, string(m.content))
		}

		return syntax.Position{
			Line:   line,
			Col:    col,
			Offset: offset,
		}, nil
	}

	lineStart := m.lineStart[pos.Line]

	lineEnd := 0
	if int(pos.Line) == len(m.lineStart)-1 {
		lineEnd = len(m.content)
	} else {
		lineEnd = m.lineStart[pos.Line+1]
	}

	lineBytes := m.content[lineStart:lineEnd]

	utf16Col := 0
	bytesCol := 0

	for len(lineBytes) > 0 {
		if utf16Col >= int(pos.Character) {
			break
		}

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

	offset := lineStart + bytesCol
	if offset > len(m.content) {
		return syntax.InvalidPosition, errors.New("invalid position character")
	}

	/*
		if offset >= m.lineStart[pos.Line+1] {
			return syntax.InvalidPosition, errors.New("invalid position character")
		}
	*/

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
