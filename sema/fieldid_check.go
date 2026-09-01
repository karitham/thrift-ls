package sema

import (
	"context"
	"strconv"

	"github.com/karitham/thrift-ls/syntax"
)

type FieldIDCheck struct{}

// FieldIDCheck checks struct, union, exception, function parameter, and
// throws field ids: they must be unique positive integers in [1, 32767].
func (c *FieldIDCheck) Name() string {
	return "FieldIDCheck"
}

func (c *FieldIDCheck) AnalyzeFile(ctx context.Context, f File) ([]Diagnostic, error) {
	pf := f.PF

	var ret []Diagnostic

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
					ret = append(ret, Diagnostic{
						Span:     spanOfToken(field.FieldID),
						Severity: SeverityError,
						Code:     CodeFieldIDRange,
						Message:  "field id should be a positive integer in [1, 32767]",
					})
				}
			}

			if len(set) == 1 {
				continue
			}

			for _, field := range set {
				ret = append(ret, Diagnostic{
					Span:     spanOfToken(field.FieldID),
					Severity: SeverityError,
					Code:     CodeFieldIDConflict,
					Message:  "field id conflict",
				})
			}
		}
	})

	return ret, nil
}
