package analyzers

import (
	"context"
	"errors"
	"slices"
	"strconv"

	"github.com/karitham/thrift-ls/sema"
	"github.com/karitham/thrift-ls/syntax"
)

type FieldIDCheck struct{}

// FieldIDCheck checks struct, union, exception, function parameter, and
// throws field ids: they must be unique positive integers in [1, 32767].
func (c *FieldIDCheck) Name() string {
	return "FieldIDCheck"
}

func (c *FieldIDCheck) AnalyzeFile(ctx context.Context, f sema.File) ([]sema.Diagnostic, error) {
	pf := f.PF

	var ret []sema.Diagnostic

	pf.AST().WalkFieldLists(func(fields []*syntax.Field, _ syntax.FieldListKind) {
		fieldIDSet := make(map[int][]*syntax.Field)

		for i := range fields {
			field := fields[i]
			if field.FieldID == nil {
				continue
			}

			value, err := strconv.ParseInt(field.FieldID.Text, 0, 32)
			if err != nil {
				// A value too large for int32 is outside [1, 32767]; a token
				// that is not an integer at all is the parser's error.
				if errors.Is(err, strconv.ErrRange) {
					ret = append(ret, fieldIDRangeDiagnostic(field))
				}

				continue
			}

			fieldIDSet[int(value)] = append(fieldIDSet[int(value)], field)
		}

		for fieldID, set := range fieldIDSet {
			if fieldID < 1 || fieldID > 32767 {
				for _, field := range set {
					ret = append(ret, fieldIDRangeDiagnostic(field))
				}
			}

			if len(set) == 1 {
				continue
			}

			for _, field := range set {
				ret = append(ret, sema.Diagnostic{
					Span:     sema.TokenSpan(field.FieldID),
					Severity: sema.SeverityError,
					Code:     sema.CodeFieldIDConflict,
					Message:  "field id conflict",
				})
			}
		}
	})

	// Field ids are grouped in a map, so emission order varies per run;
	// sort by position to keep `thrift-ls check` output reproducible.
	slices.SortStableFunc(ret, func(a, b sema.Diagnostic) int {
		if a.Span.Start.Line != b.Span.Start.Line {
			return a.Span.Start.Line - b.Span.Start.Line
		}

		return a.Span.Start.Col - b.Span.Start.Col
	})

	return ret, nil
}

// fieldIDRangeDiagnostic is the diagnostic for a field id outside [1, 32767].
func fieldIDRangeDiagnostic(field *syntax.Field) sema.Diagnostic {
	return sema.Diagnostic{
		Span:     sema.TokenSpan(field.FieldID),
		Severity: sema.SeverityError,
		Code:     sema.CodeFieldIDRange,
		Message:  "field id should be a positive integer in [1, 32767]",
	}
}
