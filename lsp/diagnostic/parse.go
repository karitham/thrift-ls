package diagnostic

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

func (p *Parse) Diagnostic(ctx context.Context, ss *cache.Snapshot, changeFiles []uri.URI) (DiagnosticResult, error) {
	var errs []error

	res := make(DiagnosticResult)
	for _, uri := range changeFiles {
		parseRes, err := ss.Parse(ctx, uri)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		for _, err := range parseRes.Errors() {
			slog.Debug("diagnostic parse failed", "err", err)
			res[uri] = append(res[uri], syntaxErrorToDiagnostic(err))
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
func syntaxErrorToDiagnostic(err syntax.Error) protocol.Diagnostic {
	severity := protocol.DiagnosticSeverityError
	if err.Severity == syntax.SeverityWarning {
		severity = protocol.DiagnosticSeverityWarning
	}
	return protocol.Diagnostic{
		Range: protocol.Range{
			Start: protocol.Position{
				Line:      uint32(err.Line - 1),
				Character: uint32(err.Col - 1),
			},
			End: protocol.Position{
				Line:      uint32(err.Line - 1),
				Character: uint32(err.Col - 1),
			},
		},
		Severity: severity,
		Source:   protocol.NewOptional("thrift-ls"),
		Message:  protocol.String(err.Message),
	}
}
