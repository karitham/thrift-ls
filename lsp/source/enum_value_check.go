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
		for _, im := range enumImplicitValues(enum) {
			msg := fmt.Sprintf("%s has no explicit value", im.member.Name.Text)
			if im.known {
				msg = fmt.Sprintf("%s has no explicit value (implicitly %d)", im.member.Name.Text, im.value)
			}

			ret = append(ret, protocol.Diagnostic{
				Range:    tokenRange(pf, enumValueNameToken(pf, im.member)),
				Severity: protocol.DiagnosticSeverityWarning,
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

	// A virtual value of -1 precedes the first member so the first
	// implicit member auto-increments to 0, mirroring the compiler.
	val, known := int64(-1), true

	for _, member := range enum.Values {
		if member.Value == nil {
			im := enumImplicitValue{member: member, known: known}
			if known {
				val++
				im.value = val
			}
			out = append(out, im)
			continue
		}

		v, err := strconv.ParseInt(member.Value.Text, 0, 64)
		if err != nil {
			known = false
			continue
		}
		val, known = v, true
	}

	return out
}
