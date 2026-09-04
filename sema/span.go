package sema

import (
	"unicode/utf8"

	"github.com/karitham/thrift-ls/syntax"
)

// TokenSpan returns a token's source span. Analyzers use this for
// token-level findings the node-based SpanOf cannot express: field IDs
// are bare tokens, not nodes.
func TokenSpan(tok *syntax.Token) Span {
	if tok == nil {
		return Span{}
	}

	start := syntax.Position{Line: tok.Line, Col: tok.Col, Offset: tok.Offset}
	end := syntax.Position{Line: tok.Line, Col: tok.Col + utf8.RuneCountInString(tok.Text), Offset: tok.Offset + len(tok.Text)}

	return Span{Start: start, End: end}
}

// LineSpan returns the span of the whole source line containing pos, the
// trailing newline included. Fixers use this to delete line-held
// statements (unused includes) whole.
func LineSpan(content []byte, pos syntax.Position) Span {
	start := pos.Offset
	for start > 0 && content[start-1] != '\n' {
		start--
	}

	end := pos.Offset
	for end < len(content) && content[end] != '\n' {
		end++
	}

	if end < len(content) {
		end++
	}

	return Span{
		Start: syntax.Position{Line: pos.Line, Col: 1, Offset: start},
		End:   syntax.Position{Line: pos.Line + 1, Col: 1, Offset: end},
	}
}
