// Package sema owns thrift-ls's semantic analysis: the lint pipeline, the
// diagnostics it produces, and the fixes attached to them. All positions
// are parser coordinates; frontends translate to their own representations.
package sema

import (
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

// Span is a half-open file region in parser coordinates (1-based line and
// rune column, byte offset). The byte offsets are authoritative; the LSP
// frontend maps them through the file mapper to UTF-16.
type Span struct {
	Start, End syntax.Position
}

// SpanOf returns the source span of a node in the parsed file.
func SpanOf(pf *cache.ParsedFile, node syntax.Node) Span {
	start, end := pf.AST().Range(node)

	return Span{Start: start, End: end}
}

// Overlaps reports whether s and o share at least one position; a cursor
// at either endpoint counts.
func (s Span) Overlaps(o Span) bool {
	return s.Start.Offset <= o.End.Offset && o.Start.Offset <= s.End.Offset
}

// Severity is a diagnostic's display weight.
type Severity uint8

const (
	SeverityError Severity = iota + 1
	SeverityWarning
	SeverityInfo
	SeverityHint
)

// Edit replaces Span with NewText in the file the diagnostic belongs to.
// An empty Span inserts.
type Edit struct {
	Span    Span
	NewText string
}

// Fix is a named set of edits resolving one diagnostic. The title is what
// the client shows in its quickfix menu.
type Fix struct {
	Title string
	Edits []Edit
}

// Diagnostic is one finding. Fixes are edits that resolve it, computed by
// the analyzer that reported the diagnostic.
type Diagnostic struct {
	Code     string // stable identity; never matched by message
	Severity Severity
	Message  string
	Span     Span
	Fixes    []Fix
}

// Report is the result of one analysis pass, keyed by file.
type Report map[uri.URI][]Diagnostic

// Action is an offered source edit: a quickfix for a diagnostic or a
// refactor.
type Action struct {
	Title string
	Fix   bool // true: quickfix for a diagnostic; false: refactor
	File  uri.URI
	Edits []Edit
}
