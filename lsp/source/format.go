package source

import (
	"bytes"
	"context"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/formatter"
	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/mapper"
)

// Format returns the whole-document formatting of fh's content.
func Format(ctx context.Context, ss *cache.Snapshot, fh cache.FileHandle, opts formatter.Options) (string, error) {
	pf, err := ss.Parse(ctx, fh.URI())
	if err != nil {
		return "", err
	}

	if len(pf.Errors()) > 0 || pf.AST() == nil {
		return "", pf.AggregatedError()
	}

	return formatter.Format(pf.AST(), opts)
}

// FormatDocument returns the single text edit replacing the whole document
// with its formatted content. It returns nil when the document is already
// formatted.
func FormatDocument(ctx context.Context, ss *cache.Snapshot, fh cache.FileHandle, opts formatter.Options) (*protocol.TextEdit, error) {
	content, err := fh.Content()
	if err != nil {
		return nil, err
	}

	formatted, err := Format(ctx, ss, fh, opts)
	if err != nil {
		return nil, err
	}

	if string(content) == formatted {
		return nil, nil
	}

	mp := mapper.NewMapper(content)
	endPos := mp.GetLSPEndPosition()

	return &protocol.TextEdit{
		Range: protocol.Range{
			Start: protocol.Position{Line: 0, Character: 0},
			End: protocol.Position{
				Line:      endPos.Line,
				Character: endPos.Character,
			},
		},
		NewText: formatted,
	}, nil
}

// FormatRange implements textDocument/rangeFormatting.
//
// The formatter only knows how to print whole documents, so a range is
// formatted by formatting the whole document and diffing it against the
// original at the granularity of blank-line-separated blocks. Blank lines
// are preserved exactly by the formatter, so the blocks align one-to-one;
// every edit is bounded by blank lines or file edges, and any subset
// splices safely. Only the edits overlapping the selection are returned.
func FormatRange(ctx context.Context, ss *cache.Snapshot, fh cache.FileHandle, opts formatter.Options, rng protocol.Range) ([]protocol.TextEdit, error) {
	content, err := fh.Content()
	if err != nil {
		return nil, err
	}

	formatted, err := Format(ctx, ss, fh, opts)
	if err != nil {
		return nil, err
	}

	if string(content) == formatted {
		return nil, nil
	}

	mp := mapper.NewMapper(content)

	start, err := mp.LSPPosToParserPosition(rng.Start)
	if err != nil {
		return nil, err
	}

	end, err := mp.LSPPosToParserPosition(rng.End)
	if err != nil {
		return nil, err
	}

	// The selection expanded to whole lines.
	selStart := lineStart(content, start.Offset)
	selEnd := nextLineStart(content, lineStart(content, end.Offset))

	var result []protocol.TextEdit

	for _, be := range blockDiff(content, []byte(formatted)) {
		// Overlap test on byte offsets; adjacent edits touch at most.
		if be.end <= selStart || be.start >= selEnd {
			continue
		}

		startPos, err := mp.OffsetToLSPPosition(be.start)
		if err != nil {
			return nil, err
		}

		endPos, err := mp.OffsetToLSPPosition(be.end)
		if err != nil {
			return nil, err
		}

		result = append(result, protocol.TextEdit{
			Range: protocol.Range{
				Start: startPos,
				End:   endPos,
			},
			NewText: be.text,
		})
	}

	return result, nil
}

// blockEdit replaces content[start:end] with text. Every block edit is
// bounded by blank lines or file edges, so it splices safely.
type blockEdit struct {
	start, end int
	text       string
}

// blockDiff returns the edits turning old into new, one per changed
// segment: the blank-line runs and the blocks of non-blank lines between
// them. Blank lines are preserved structurally by the formatter (their
// whitespace may be trimmed), so old and new split into the same number of
// aligned blocks; every edit is bounded by blank lines or file edges, so
// any subset splices safely.
func blockDiff(old, new []byte) []blockEdit {
	// CRLF input normalizes to LF everywhere, blank lines included: the
	// block alignment no longer holds, so a single whole-document edit is
	// the only safe splice.
	if bytes.Contains(old, []byte("\r\n")) {
		if string(old) == string(new) {
			return nil
		}

		return []blockEdit{{0, len(old), string(new)}}
	}

	oldBlocks := blocks(old)
	newBlocks := blocks(new)

	// No non-blank lines at all, or an unaligned block structure: fall
	// back to a single whole-document edit.
	if len(oldBlocks) == 0 || len(oldBlocks) != len(newBlocks) {
		if string(old) == string(new) {
			return nil
		}

		return []blockEdit{{0, len(old), string(new)}}
	}

	var edits []blockEdit

	prevOld, prevNew := 0, 0
	for i := range oldBlocks {
		// The segment before the block: leading blanks, or the blank run
		// between two blocks.
		if !bytes.Equal(old[prevOld:oldBlocks[i].start], new[prevNew:newBlocks[i].start]) {
			edits = append(edits, blockEdit{
				start: prevOld,
				end:   oldBlocks[i].start,
				text:  string(new[prevNew:newBlocks[i].start]),
			})
		}

		// The block itself.
		if !bytes.Equal(old[oldBlocks[i].start:oldBlocks[i].end], new[newBlocks[i].start:newBlocks[i].end]) {
			edits = append(edits, blockEdit{
				start: oldBlocks[i].start,
				end:   oldBlocks[i].end,
				text:  string(new[newBlocks[i].start:newBlocks[i].end]),
			})
		}

		prevOld, prevNew = oldBlocks[i].end, newBlocks[i].end
	}

	// The trailing segment.
	if !bytes.Equal(old[prevOld:], new[prevNew:]) {
		edits = append(edits, blockEdit{prevOld, len(old), string(new[prevNew:])})
	}

	return edits
}

// block is a maximal run of non-blank lines: the byte range from the first
// line's start to just after the last line's newline, with the exact text.
type block struct {
	start, end int
	text       string
}

// blocks splits content into runs of non-blank lines.
func blocks(content []byte) []block {
	var out []block

	i := 0
	for i < len(content) {
		// Skip blank lines.
		for i < len(content) && len(bytes.TrimSpace(content[i:lineEnd(content, i)])) == 0 {
			i = nextLineStart(content, i)
		}

		if i >= len(content) {
			break
		}

		start := i
		for i < len(content) && len(bytes.TrimSpace(content[i:lineEnd(content, i)])) > 0 {
			i = nextLineStart(content, i)
		}

		out = append(out, block{start: start, end: i, text: string(content[start:i])})
	}

	return out
}

// nextLineStart returns the offset just after the newline ending the line
// containing offset, or len(content) for the last line.
func nextLineStart(content []byte, offset int) int {
	if i := bytes.IndexByte(content[offset:], '\n'); i != -1 {
		return offset + i + 1
	}

	return len(content)
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
