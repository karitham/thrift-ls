package source

import "go.lsp.dev/protocol"

// Diagnostic codes carried on every diagnostic thrift-ls publishes. Code
// actions match on these — never on the message text, which is free to
// change.
const (
	CodeParseError        = "parse-error"
	CodeIncludeCycle      = "include-cycle"
	CodeFieldIDRange      = "field-id-range"
	CodeFieldIDConflict   = "field-id-conflict"
	CodeDuplicateDef      = "duplicate-definition"
	CodeDuplicateEnumVal  = "duplicate-enum-value"
	CodeDuplicateValue    = "duplicate-value"
	CodeImplicitEnumValue = "implicit-enum-value"
	CodeUnusedInclude     = "unused-include"
	CodeIncludeShadow     = "include-shadow"
	CodeUndefinedType     = "undefined-type"
	CodeUndefinedValue    = "undefined-value"
	CodeValueTypeMismatch = "value-type-mismatch"
	CodeNonScalarMapKey   = "non-scalar-map-key"
	CodeUnknownAnnotation = "unknown-annotation-type"
)

// hasCode reports whether the diagnostic carries code. Diagnostics reach
// code actions through the client, which echoes the code back as a
// protocol.String.
func hasCode(d protocol.Diagnostic, code string) bool {
	s, ok := d.Code.(protocol.String)

	return ok && string(s) == code
}

// RangesOverlap reports whether two ranges share at least one position,
// degenerate single-point ranges included: a cursor at either endpoint of a
// diagnostic's range counts as overlapping it.
func RangesOverlap(a, b protocol.Range) bool {
	return positionBefore(a.Start, b.End) && positionBefore(b.Start, a.End)
}

// positionBefore reports a <= b.
func positionBefore(a, b protocol.Position) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}

	return a.Character <= b.Character
}
