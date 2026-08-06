package lsp

import (
	"bytes"
	"context"
	"strings"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/formatter"
	"github.com/karitham/thrift-ls/lsp/mapper"
	"github.com/karitham/thrift-ls/lsp/types"
	"github.com/karitham/thrift-ls/syntax"
)

func (s *Server) formatting(ctx context.Context, params *protocol.DocumentFormattingParams) (result []protocol.TextEdit, err error) {
	opts := s.formatOpts

	document := params.TextDocument
	fileURI := document.URI
	view, err := s.session.ViewOf(fileURI)
	if err != nil {
		return nil, err
	}

	ss, release := view.Snapshot()
	defer release()

	fh, err := ss.ReadFile(ctx, fileURI)
	if err != nil {
		return nil, err
	}

	bytes, err := fh.Content()
	if err != nil {
		return nil, err
	}

	pf, err := ss.Parse(ctx, fileURI)
	if err != nil {
		return nil, err
	}
	if len(pf.Errors()) > 0 || pf.AST() == nil {
		return nil, pf.AggregatedError()
	}

	formatted, err := formatter.Format(pf.AST(), opts)
	if err != nil {
		return nil, err
	}

	mp := mapper.NewMapper(fileURI, bytes)
	endPos := mp.GetLSPEndPosition()
	textEdit := protocol.TextEdit{
		Range: protocol.Range{
			Start: protocol.Position{
				Line:      0,
				Character: 0,
			},
			End: protocol.Position{
				Line:      endPos.Line,
				Character: endPos.Character,
			},
		},
		NewText: formatted,
	}

	result = append(result, textEdit)

	return result, err
}

// rangeFormatting implements textDocument/rangeFormatting.
//
// The formatter only knows how to print whole documents, so a range is
// formatted by extracting the selected slice, formatting it as a standalone
// document, and splicing the result back into the file. Following Prettier's
// range-formatting contract, the range must be bounded by blank lines (or
// file edges) after expansion to line boundaries; otherwise no edits are
// produced and the request is a no-op.
func (s *Server) rangeFormatting(ctx context.Context, params *protocol.DocumentRangeFormattingParams) (result []protocol.TextEdit, err error) {
	fileURI := params.TextDocument.URI
	view, err := s.session.ViewOf(fileURI)
	if err != nil {
		return nil, err
	}

	ss, release := view.Snapshot()
	defer release()

	fh, err := ss.ReadFile(ctx, fileURI)
	if err != nil {
		return nil, err
	}

	content, err := fh.Content()
	if err != nil {
		return nil, err
	}

	mp := mapper.NewMapper(fileURI, content)
	start, err := mp.LSPPosToParserPosition(lspPosition(params.Range.Start))
	if err != nil {
		return nil, nil
	}
	end, err := mp.LSPPosToParserPosition(lspPosition(params.Range.End))
	if err != nil {
		return nil, nil
	}
	rs, re := start.Offset, end.Offset
	if rs >= re {
		return nil, nil
	}

	// A selection covering the whole document delegates to full formatting.
	if rs == 0 && re == len(content) {
		return s.formatting(ctx, &protocol.DocumentFormattingParams{TextDocument: params.TextDocument})
	}

	newText, rs, re, ok := formatRangeText(content, rs, re, s.formatOpts)
	if !ok {
		return nil, nil
	}

	startPos, err := mp.OffsetToLSPPosition(rs)
	if err != nil {
		return nil, nil
	}
	endPos, err := mp.OffsetToLSPPosition(re)
	if err != nil {
		return nil, nil
	}

	return []protocol.TextEdit{{
		Range: protocol.Range{
			Start: protocolPosition(startPos),
			End:   protocolPosition(endPos),
		},
		NewText: newText,
	}}, nil
}

// lspPosition converts a protocol position to the internal position type.
func lspPosition(p protocol.Position) types.Position {
	return types.Position{
		Line:      uint32(p.Line),
		Character: uint32(p.Character),
	}
}

// protocolPosition converts an internal position to a protocol position.
func protocolPosition(p types.Position) protocol.Position {
	return protocol.Position{
		Line:      p.Line,
		Character: p.Character,
	}
}

// formatRangeText formats content[rs:re] (half-open byte offsets) and returns
// the replacement text. ok is false when the range is not safely bounded by
// blank lines or file edges, or the slice does not parse cleanly.
func formatRangeText(content []byte, rs, re int, opts formatter.Options) (newText string, outRS, outRE int, ok bool) {
	// An empty or inverted selection produces no edits.
	if rs >= re {
		return "", rs, re, false
	}

	// Expand to line boundaries, then trim leading and trailing blank lines
	// from the selection.
	rs = lineStart(content, rs)
	re = lineEnd(content, re)
	rs = skipBlankLinesForward(content, rs, re)
	re = skipBlankLinesBackward(content, rs, re)
	if rs >= re {
		return "", rs, re, false
	}

	// The lines immediately outside the range must be blank, or the range
	// must touch a file edge.
	if !blankLineBefore(content, rs) || !blankLineAfter(content, re) {
		return "", rs, re, false
	}

	slice := content[rs:re]
	doc, errs := syntax.Parse(slice)
	if len(errs) > 0 {
		return "", rs, re, false
	}

	formatted, err := formatter.Format(doc, opts)
	if err != nil {
		return "", rs, re, false
	}

	// The splice must not add or drop newlines at the boundaries: the slice
	// starts at a line start and ends just before its last line's newline.
	formatted = strings.TrimLeft(formatted, "\n")
	formatted = strings.TrimRight(formatted, "\r\n")
	return formatted, rs, re, true
}

// lineStart returns the byte offset of the start of the line containing offset.
func lineStart(content []byte, offset int) int {
	if i := bytes.LastIndexByte(content[:offset], '\n'); i != -1 {
		return i + 1
	}
	return 0
}

// lineEnd returns the byte offset of the newline ending the line containing
// offset, or len(content) for the last line.
func lineEnd(content []byte, offset int) int {
	if i := bytes.IndexByte(content[offset:], '\n'); i != -1 {
		return offset + i
	}
	return len(content)
}

// blankLineBefore reports whether the line before the line starting at offset
// is blank (whitespace only) or offset is at the start of the file.
func blankLineBefore(content []byte, offset int) bool {
	if offset == 0 {
		return true
	}
	start := lineStart(content, offset-1)
	return len(bytes.TrimSpace(content[start:offset])) == 0
}

// blankLineAfter reports whether the line after the one ending at offset is
// blank (whitespace only) or offset is at the end of the file.
func blankLineAfter(content []byte, offset int) bool {
	if offset == len(content) {
		return true
	}
	end := lineEnd(content, offset+1)
	return len(bytes.TrimSpace(content[offset+1:end])) == 0
}

// skipBlankLinesForward advances offset past blank lines, stopping at limit.
func skipBlankLinesForward(content []byte, offset, limit int) int {
	for offset < limit {
		end := lineEnd(content, offset)
		if end >= limit {
			break
		}
		if len(bytes.TrimSpace(content[offset:end])) > 0 {
			break
		}
		offset = end + 1
	}
	return offset
}

// skipBlankLinesBackward retreats offset (a newline position or len(content))
// past blank lines, stopping at start.
func skipBlankLinesBackward(content []byte, start, offset int) int {
	for offset > start {

		lineStart := lineStart(content, offset-1)
		if len(bytes.TrimSpace(content[lineStart:offset])) > 0 {
			break
		}
		offset = lineStart - 1
		if offset < 0 {
			offset = 0
		}
	}
	return offset
}
