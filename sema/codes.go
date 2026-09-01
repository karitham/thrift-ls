package sema

// Diagnostic codes carried on every diagnostic the pipeline reports. Fix
// providers and frontend filters match on these — never on the message
// text, which is free to change.
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
