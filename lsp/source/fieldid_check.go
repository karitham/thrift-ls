package source

import (
	"context"
	"errors"
	"log/slog"
	"strconv"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

type FieldIDCheck struct{}

// FieldIDCheck checks struct, union, exception, function parameter, and
// throws field ids: they must be unique positive integers in [1, 32767].
func (c *FieldIDCheck) Diagnostic(ctx context.Context, ss *cache.Snapshot, changeFiles []uri.URI) (DiagnosticResult, error) {
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

func (c *FieldIDCheck) Name() string {
	return "FieldIDCheck"
}

func (c *FieldIDCheck) diagnostic(ctx context.Context, ss *cache.Snapshot, file uri.URI) ([]protocol.Diagnostic, error) {
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

	processStructLike := func(fields []*syntax.Field) {
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
					ret = append(ret, protocol.Diagnostic{
						Range:    tokenRange(pf, field.FieldID),
						Severity: protocol.DiagnosticSeverityError,
						Source:   protocol.NewOptional("thrift-ls"),
						Message:  protocol.String("field id should be a positive integer in [1, 32767]"),
					})
				}
			}

			if len(set) == 1 {
				continue
			}

			for _, field := range set {
				ret = append(ret, protocol.Diagnostic{
					Range:    tokenRange(pf, field.FieldID),
					Severity: protocol.DiagnosticSeverityError,
					Source:   protocol.NewOptional("thrift-ls"),
					Message:  protocol.String("field id conflict"),
				})
			}
		}
	}

	for _, st := range pf.AST().Structs() {
		processStructLike(st.Fields)
	}

	for _, union := range pf.AST().Unions() {
		processStructLike(union.Fields)
	}

	for _, excep := range pf.AST().Exceptions() {
		processStructLike(excep.Fields)
	}

	for _, svc := range pf.AST().Services() {
		for _, fn := range svc.Functions {
			processStructLike(fn.Args)

			if fn.Throws != nil {
				processStructLike(fn.Throws.Fields)
			}
		}
	}

	return ret, nil
}
