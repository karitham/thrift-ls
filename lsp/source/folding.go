// Package folding computes document folding ranges: braced bodies
// (structs, enums, services), const list and map values, annotations, and
// comment blocks. Pure over the snapshot: parsing and file I/O happen in
// the caller.
package source

import (
	"context"
	"sort"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

// Ranges returns the folding ranges of a file, in source order. Degenerate
// single-line ranges are omitted.
func Ranges(ctx context.Context, ss *cache.Snapshot, file uri.URI) []protocol.FoldingRange {
	pf, err := ss.Parse(ctx, file)
	if err != nil || pf.AST() == nil {
		return nil
	}

	doc := pf.AST()
	ranges := make([]protocol.FoldingRange, 0, 16)

	for _, node := range doc.Nodes {
		switch v := node.(type) {
		case *syntax.Struct, *syntax.Enum, *syntax.Service:
			if r, ok := bracedRange(doc, node); ok {
				ranges = append(ranges, r)
			}
		case *syntax.Const:
			if v.Value != nil {
				switch v.Value.Kind {
				case syntax.ValueList, syntax.ValueMap:
					if r, ok := spanRange(doc, v.Value.TokStart(), v.Value.TokEnd()); ok {
						ranges = append(ranges, r)
					}
				}
			}
		}
	}

	if ann := nodeAnnotations(doc, doc.Nodes); len(ann) > 0 {
		for _, a := range ann {
			if r, ok := spanRange(doc, a.TokStart(), a.TokEnd()); ok {
				ranges = append(ranges, r)
			}
		}
	}

	ranges = append(ranges, commentBlocks(doc)...)

	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].StartLine != ranges[j].StartLine {
			return ranges[i].StartLine < ranges[j].StartLine
		}

		return startChar(ranges[i]) < startChar(ranges[j])
	})

	return ranges
}

// startChar returns the start character of a range, defaulting to 0.
func startChar(r protocol.FoldingRange) uint32 {
	if r.StartCharacter == nil {
		return 0
	}

	return *r.StartCharacter
}

// bracedRange returns the fold range of a brace-delimited body: from the
// opening brace to the closing one.
func bracedRange(doc *syntax.Document, n syntax.Node) (protocol.FoldingRange, bool) {
	open := -1

	for i := n.TokStart(); i <= n.TokEnd(); i++ {
		if doc.Tokens[i].Kind == syntax.TokenLBrace {
			open = i

			break
		}
	}

	if open < 0 {
		return protocol.FoldingRange{}, false
	}

	close := open
	for i := n.TokEnd(); i > open; i-- {
		if doc.Tokens[i].Kind == syntax.TokenRBrace {
			close = i

			break
		}
	}

	return spanRange(doc, open, close)
}

// nodeAnnotations collects the annotations of every top-level node, in
// source order.
func nodeAnnotations(doc *syntax.Document, nodes []syntax.Node) []*syntax.Annotations {
	var anns []*syntax.Annotations

	for _, n := range nodes {
		if ann := nodeAnnotation(n); ann != nil {
			anns = append(anns, ann)
		}
	}

	return anns
}

func nodeAnnotation(n syntax.Node) *syntax.Annotations {
	switch v := n.(type) {
	case *syntax.Struct:
		return v.Annotations
	case *syntax.Enum:
		return v.Annotations
	case *syntax.Service:
		return v.Annotations
	case *syntax.Const:
		return nil
	case *syntax.Typedef:
		return v.Annotations
	case *syntax.Namespace:
		return v.Annotations
	}

	return nil
}

// spanRange converts the token span [start, end] into a folding range.
// Degenerate single-line spans yield no range.
func spanRange(doc *syntax.Document, start, end int) (protocol.FoldingRange, bool) {
	s := doc.TokenPosition(start)
	e := doc.TokenEndPosition(end)

	if s.Line == e.Line {
		return protocol.FoldingRange{}, false
	}

	return protocol.FoldingRange{
		StartLine:      uint32(s.Line - 1),
		StartCharacter: new(uint32(s.Col - 1)),
		EndLine:        uint32(e.Line - 1),
		EndCharacter:   new(uint32(e.Col - 1)),
	}, true
}

// commentSpanRange is spanRange for comment folds.
func commentSpanRange(doc *syntax.Document, start, end int) (protocol.FoldingRange, bool) {
	r, ok := spanRange(doc, start, end)
	if ok {
		r.Kind = protocol.FoldingRangeKindComment
	}

	return r, ok
}

// blockCommentSpan folds a multi-line block comment. The token records
// only its start position, so the end line is derived from the text.
func blockCommentSpan(doc *syntax.Document, idx int) (protocol.FoldingRange, bool) {
	tok := doc.Tokens[idx]
	start := doc.TokenPosition(idx)

	lines := strings.Count(tok.Text, "\n")
	if lines == 0 {
		return protocol.FoldingRange{}, false
	}

	last := tok.Text[strings.LastIndex(tok.Text, "\n")+1:]

	return protocol.FoldingRange{
		StartLine:      uint32(start.Line - 1),
		StartCharacter: new(uint32(start.Col - 1)),
		EndLine:        uint32(start.Line - 1 + lines),
		EndCharacter:   new(uint32(len(last))),
		Kind:           protocol.FoldingRangeKindComment,
	}, true
}

// commentBlocks folds consecutive same-line comments on consecutive
// source lines, and multi-line block comments, into comment fold ranges.
func commentBlocks(doc *syntax.Document) []protocol.FoldingRange {
	var ranges []protocol.FoldingRange

	for i := 0; i < len(doc.Tokens); i++ {
		tok := doc.Tokens[i]
		if !isLineComment(tok.Kind) {
			if tok.Kind == syntax.TokenBlockComment || tok.Kind == syntax.TokenDocComment {
				if r, ok := blockCommentSpan(doc, i); ok {
					ranges = append(ranges, r)
				}
			}

			continue
		}

		// A run of line comments on consecutive lines folds as a block.
		start := i
		for i+1 < len(doc.Tokens) && isLineComment(doc.Tokens[i+1].Kind) && doc.Tokens[i+1].Line == doc.Tokens[i].Line+1 {
			i++
		}

		if i > start {
			if r, ok := commentSpanRange(doc, start, i); ok {
				ranges = append(ranges, r)
			}
		}
	}

	return ranges
}

func isLineComment(k syntax.TokenKind) bool {
	return k == syntax.TokenLineComment || k == syntax.TokenAnnotation
}
