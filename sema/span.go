package sema

import (
	"unicode/utf8"

	"github.com/karitham/thrift-ls/syntax"
)

// spanOfToken returns a token's source span.
func spanOfToken(tok *syntax.Token) Span {
	if tok == nil {
		return Span{}
	}

	start := syntax.Position{Line: tok.Line, Col: tok.Col, Offset: tok.Offset}
	end := syntax.Position{Line: tok.Line, Col: tok.Col + utf8.RuneCountInString(tok.Text), Offset: tok.Offset + len(tok.Text)}

	return Span{Start: start, End: end}
}
