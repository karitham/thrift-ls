package source

import (
	"context"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/sema"
)

// ToProtocolDiagnostics translates one file's pipeline findings into LSP
// wire diagnostics: spans map through the file's mapper to UTF-16
// columns, severities map onto the protocol scale, and the Source field
// is set here — it is LSP presentation, not core data.
func ToProtocolDiagnostics(ctx context.Context, view *cache.View, file uri.URI, diags []sema.Diagnostic) ([]protocol.Diagnostic, error) {
	pf, err := view.Parse(ctx, file)
	if err != nil {
		return nil, err
	}

	out := make([]protocol.Diagnostic, 0, len(diags))

	for _, d := range diags {
		out = append(out, protocol.Diagnostic{
			Range:    toLSPRange(pf, d.Span.Start, d.Span.End),
			Severity: protocol.DiagnosticSeverity(d.Severity),
			Code:     protocol.String(d.Code),
			Source:   protocol.NewOptional("thrift-ls"),
			Message:  protocol.String(d.Message),
		})
	}

	return out, nil
}
