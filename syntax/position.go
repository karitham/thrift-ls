package syntax

import "unicode/utf8"

// Position is a source position: 1-based line, 1-based rune column, and
// 0-based byte offset.
type Position struct {
	Line   int
	Col    int
	Offset int
}

// InvalidPosition is the zero Position.
var InvalidPosition = Position{}

// IsValid reports whether the position has a line number.
func (p Position) IsValid() bool {
	return p.Line > 0
}

// TokenPosition returns the start position of token i.
func (d *Document) TokenPosition(i int) Position {
	t := d.Tokens[i]
	return Position{Line: t.Line, Col: t.Col, Offset: t.Offset}
}

// TokenEndPosition returns the position immediately after token i. Tokens
// never span lines, so the end position is on the same line.
func (d *Document) TokenEndPosition(i int) Position {
	t := d.Tokens[i]
	return Position{
		Line:   t.Line,
		Col:    t.Col + utf8.RuneCountInString(t.Text),
		Offset: t.Offset + len(t.Text),
	}
}

// TokenIndex returns the index of a token pointer in the document's token
// stream. Token pointers are stable: the parser stores pointers into the
// document's token slice. It returns 0 for nil or foreign tokens.
func (d *Document) TokenIndex(t *Token) int {
	if t == nil {
		return 0
	}
	for i := range d.Tokens {
		if &d.Tokens[i] == t {
			return i
		}
	}
	return 0
}

// TokenRange returns the span of a token.
func (d *Document) TokenRange(t *Token) (start, end Position) {
	i := d.TokenIndex(t)
	return d.TokenPosition(i), d.TokenEndPosition(i)
}

// Range returns the span of a node: from the start of its first token to
// the end of its last token.
func (d *Document) Range(n Node) (start, end Position) {
	return d.TokenPosition(n.TokStart()), d.TokenEndPosition(n.TokEnd())
}

// Contains reports whether pos lies within the node's span, inclusive.
func (d *Document) Contains(n Node, pos Position) bool {
	start, end := d.Range(n)
	return pos.Offset >= start.Offset && pos.Offset <= end.Offset
}
