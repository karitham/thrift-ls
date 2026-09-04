package analyzers

import (
	"context"
	"fmt"
	"strconv"

	"github.com/karitham/thrift-ls/sema"
)

type EnumValueCheck struct{}

// EnumValueCheck warns on enum members that lack an explicit value.
//
// The compiler auto-increments implicit members: 0 for the first member,
// one greater than the preceding member's value otherwise. Their on-wire
// value therefore follows their position; inserting, removing, or
// reordering members silently changes serialized data.
func (c *EnumValueCheck) Name() string {
	return "EnumValueCheck"
}

func (c *EnumValueCheck) AnalyzeFile(ctx context.Context, f sema.File) ([]sema.Diagnostic, error) {
	pf := f.PF

	var ret []sema.Diagnostic

	for _, enum := range pf.AST().Enums() {
		// A member whose name already appeared earlier in the enum is a
		// duplicate; DuplicateCheck reports it as an error. Skip the
		// implicit value warning for it: making a duplicate explicit
		// does not fix it, and its auto-incremented value is incidental.
		firstIdx := make(map[string]int, len(enum.Values))
		for i, member := range enum.Values {
			if _, ok := firstIdx[member.Name.Text]; !ok {
				firstIdx[member.Name.Text] = i
			}
		}

		for i, mv := range sema.EnumMemberValues(enum) {
			if mv.Member.Value != nil || firstIdx[mv.Member.Name.Text] != i {
				continue
			}

			msg := fmt.Sprintf("%s has no explicit value", mv.Member.Name.Text)
			if mv.Known {
				msg = fmt.Sprintf("%s has no explicit value (implicitly %d)", mv.Member.Name.Text, mv.Value)
			}

			d := sema.Diagnostic{
				Span:     sema.SpanOf(pf, mv.Member.Name),
				Severity: sema.SeverityWarning,
				Code:     sema.CodeImplicitEnumValue,
				Message:  msg,
			}

			// The fix writes the auto-incremented value into the source:
			// " = N" right after the member's name. Only a member whose
			// value is known is fixable; one after a broken constant has
			// no computable value to write.
			if mv.Known {
				insertAt := pf.AST().TokenEndPosition(mv.Member.Name.TokStart())

				d.Fixes = []sema.Fix{{
					Title: fmt.Sprintf("Add explicit value %d to %s", mv.Value, mv.Member.Name.Text),
					Edits: []sema.Edit{{
						Span:    sema.Span{Start: insertAt, End: insertAt},
						NewText: " = " + strconv.FormatInt(mv.Value, 10),
					}},
				}}
			}

			ret = append(ret, d)
		}
	}

	return ret, nil
}
