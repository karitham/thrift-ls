package analyzers

import (
	"context"
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
				continue
			}

			fieldIDSet[int(value)] = append(fieldIDSet[int(value)], field)
		}

		for fieldID, set := range fieldIDSet {
			if fieldID < 1 || fieldID > 32767 {
				for _, field := range set {
					ret = append(ret, sema.Diagnostic{
						Span:     sema.TokenSpan(field.FieldID),
						Severity: sema.SeverityError,
						Code:     sema.CodeFieldIDRange,
						Message:  "field id should be a positive integer in [1, 32767]",
					})
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

	return ret, nil
}
