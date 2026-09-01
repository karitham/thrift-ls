package sema

import (
	"context"
	"errors"
	"log/slog"

	"github.com/karitham/thrift-ls/syntax"
)

// ParseCheck reports the lexer's and parser's errors and warnings for
// every changed file. It is a whole-run analyzer because it must see
// files whose AST is too broken for the other analyzers — they skip
// those, this one reports them.
type ParseCheck struct{}

func (p *ParseCheck) Name() string {
	return "Parse"
}

func (p *ParseCheck) Analyze(ctx context.Context, run *Run) error {
	var errs []error

	for _, file := range run.Files() {
		pf, err := run.view.Parse(ctx, file)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		for _, e := range pf.Errors() {
			slog.Debug("parse diagnostic", "err", e)
			run.Add(file, parseErrorDiagnostic(e))
		}
	}

	return errors.Join(errs...)
}

// parseErrorDiagnostic converts a syntax error or warning to a diagnostic
// at the error's position.
func parseErrorDiagnostic(err syntax.Error) Diagnostic {
	severity := SeverityError
	if err.Severity == syntax.SeverityWarning {
		severity = SeverityWarning
	}

	pos := syntax.Position{Line: err.Line, Col: err.Col, Offset: err.Offset}

	return Diagnostic{
		Span:     Span{Start: pos, End: pos},
		Severity: severity,
		Code:     CodeParseError,
		Message:  err.Message,
	}
}
