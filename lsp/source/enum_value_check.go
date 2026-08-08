package source

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

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
func (c *EnumValueCheck) Diagnostic(ctx context.Context, ss *cache.Snapshot, changeFiles []uri.URI) (DiagnosticResult, error) {
	res := make(DiagnosticResult)

	for _, file := range changeFiles {
		items, err := c.diagnostic(ctx, ss, file)
		if err != nil {
			return nil, err
		}

		res[file] = items
	}

	return res, nil
}

func (c *EnumValueCheck) Name() string {
	return "EnumValueCheck"
}

func (c *EnumValueCheck) diagnostic(ctx context.Context, ss *cache.Snapshot, file uri.URI) ([]protocol.Diagnostic, error) {
	pf, err := ss.Parse(ctx, file)
	if err != nil {
		return nil, err
	}

	if pf.AST() == nil {
		return nil, errors.New("parse ast failed")
	}

	for _, err := range pf.Errors() {
		slog.Debug("parse failed", "err", err)
	}

	var ret []protocol.Diagnostic

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

			ret = append(ret, protocol.Diagnostic{
				Range:    tokenRange(pf, enumValueNameToken(pf, mv.member)),
				Severity: protocol.DiagnosticSeverityWarning,
				Code:     protocol.String(CodeImplicitEnumValue),
				Source:   protocol.NewOptional("thrift-ls"),
				Message:  protocol.String(msg),
			})
		}
	}

	return ret, nil
}

// enumValueNameToken returns the name token of an enum member.
func enumValueNameToken(pf *cache.ParsedFile, v *syntax.EnumValue) *syntax.Token {
	return &pf.AST().Tokens[v.Name.TokStart()]
}

// enumImplicitValue is an enum member that lacks an explicit value, with
// the int constant the compiler auto-increments for it.
type enumImplicitValue struct {
	member *syntax.EnumValue
	value  int64
	known  bool // false when the preceding value is broken, so value is unknowable
}

// enumImplicitValues reports the members of an enum that carry no explicit
// value, together with the value the compiler would auto-increment: 0 for
// the first member, one greater than the preceding member's value
// otherwise. Members after an unparseable explicit constant report
// known=false until the next parseable constant settles the chain.
func enumImplicitValues(enum *syntax.Enum) []enumImplicitValue {
	var out []enumImplicitValue

	for _, mv := range enumValues(enum) {
		if mv.member.Value != nil {
			continue
		}

		out = append(out, enumImplicitValue(mv))
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
