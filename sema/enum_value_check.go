package sema

import (
	"context"
	"fmt"
	"strconv"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
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

func (c *EnumValueCheck) AnalyzeFile(ctx context.Context, f File) ([]Diagnostic, error) {
	pf := f.PF

	var ret []Diagnostic

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

		for i, mv := range enumValues(enum) {
			if mv.member.Value != nil || firstIdx[mv.member.Name.Text] != i {
				continue
			}

			msg := fmt.Sprintf("%s has no explicit value", mv.member.Name.Text)
			if mv.known {
				msg = fmt.Sprintf("%s has no explicit value (implicitly %d)", mv.member.Name.Text, mv.value)
			}

			d := Diagnostic{
				Span:     spanOfToken(enumValueNameToken(pf, mv.member)),
				Severity: SeverityWarning,
				Code:     CodeImplicitEnumValue,
				Message:  msg,
			}

			// The fix writes the auto-incremented value into the source:
			// " = N" right after the member's name. Only a member whose
			// value is known is fixable; one after a broken constant has
			// no computable value to write.
			if mv.known {
				insertAt := pf.AST().TokenEndPosition(mv.member.Name.TokStart())

				d.Fixes = []Fix{{
					Title: fmt.Sprintf("Add explicit value %d to %s", mv.value, mv.member.Name.Text),
					Edits: []Edit{{
						Span:    Span{Start: insertAt, End: insertAt},
						NewText: " = " + strconv.FormatInt(mv.value, 10),
					}},
				}}
			}

			ret = append(ret, d)
		}
	}

	return ret, nil
}

// enumValueNameToken returns the name token of an enum member.
func enumValueNameToken(pf *cache.ParsedFile, v *syntax.EnumValue) *syntax.Token {
	return &pf.AST().Tokens[v.Name.TokStart()]
}

// EnumImplicitValue is an enum member that lacks an explicit value, with
// the int constant the compiler auto-increments for it.
type EnumImplicitValue struct {
	Member *syntax.EnumValue
	Value  int64
	Known  bool // false when the preceding value is broken, so Value is unknowable
}

// EnumImplicitValues reports the members of an enum that carry no explicit
// value, together with the value the compiler would auto-increment: 0 for
// the first member, one greater than the preceding member's value
// otherwise. Members after an unparseable explicit constant report
// Known=false until the next parseable constant settles the chain.
func EnumImplicitValues(enum *syntax.Enum) []EnumImplicitValue {
	var out []EnumImplicitValue

	for _, mv := range enumValues(enum) {
		if mv.member.Value != nil {
			continue
		}

		out = append(out, EnumImplicitValue{Member: mv.member, Value: mv.value, Known: mv.known})
	}

	return out
}

// enumMemberValue is an enum member with the value the compiler resolves
// for it.
type enumMemberValue struct {
	member *syntax.EnumValue
	value  int64
	known  bool // false when the preceding value is broken, so value is unknowable
}

// enumValues resolves every member of an enum to its on-wire value:
// explicit constants parse as written (base-0), implicit members
// auto-increment: 0 for the first member, one greater than the preceding
// member's value otherwise. Members after an unparseable explicit constant
// report known=false until the next parseable constant settles the chain.
func enumValues(enum *syntax.Enum) []enumMemberValue {
	var out []enumMemberValue

	// A virtual value of -1 precedes the first member so the first
	// implicit member auto-increments to 0, mirroring the compiler.
	val, known := int64(-1), true

	for _, member := range enum.Values {
		if member.Value == nil {
			if known {
				val++
			}

			out = append(out, enumMemberValue{member: member, value: val, known: known})
			continue
		}

		v, err := strconv.ParseInt(member.Value.Text, 0, 64)
		if err != nil {
			out = append(out, enumMemberValue{member: member, known: false})
			known = false
			continue
		}

		val, known = v, true
		out = append(out, enumMemberValue{member: member, value: val, known: true})
	}

	return out
}
