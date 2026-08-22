package source

import (
	"context"
	"errors"
	"log/slog"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

type Parse struct{}

func (p *Parse) Diagnostic(ctx context.Context, b *Batch, changeFiles []uri.URI) (DiagnosticResult, error) {
	view := b.View()

	var errs []error

	res := make(DiagnosticResult)

	for _, uri := range changeFiles {
		parseRes, err := view.Parse(ctx, uri)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		for _, err := range parseRes.Errors() {
			slog.Debug("diagnostic parse failed", "err", err)
			res[uri] = append(res[uri], syntaxErrorToDiagnostic(parseRes, err))
		}
	}

	if len(errs) > 0 {
		return res, errors.Join(errs...)
	}

	return res, nil
}

func (p *Parse) Name() string {
	return "Parse"
}

// syntaxErrorToDiagnostic converts a syntax error or warning to an LSP
// diagnostic.
func syntaxErrorToDiagnostic(pf *cache.ParsedFile, err syntax.Error) protocol.Diagnostic {
	severity := protocol.DiagnosticSeverityError
	if err.Severity == syntax.SeverityWarning {
		severity = protocol.DiagnosticSeverityWarning
	}

	pos := toLSPPosition(pf, syntax.Position{Line: err.Line, Col: err.Col, Offset: err.Offset})

	return protocol.Diagnostic{
		Range: protocol.Range{
			Start: pos,
			End:   pos,
		},
		Severity: severity,
		Code:     protocol.String(CodeParseError),
		Source:   protocol.NewOptional("thrift-ls"),
		Message:  protocol.String(err.Message),
	}
}
