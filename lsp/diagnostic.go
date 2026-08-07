package lsp

import (
	"context"
	"errors"
	"log/slog"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/diagnostic"
)

func (s *Server) diagnostic(ctx context.Context, ss *cache.Snapshot, changeFile *cache.FileChange) error {
	if s.client == nil {
		return nil
	}

	slog.Debug("diagnostic called")
	defer slog.Debug("diagnostic finished")

	diag := diagnostic.NewDiagnostic()

	diagRes, err := diag.Diagnostic(ctx, ss, []uri.URI{changeFile.URI})
	if err != nil {
		slog.Error("diagnostic failed", "err", err)
	}

	slog.Debug("publish diagnostic result", "count", len(diagRes))

	var errs []error

	for file, res := range diagRes {
		if res == nil {
			res = make([]protocol.Diagnostic, 0)
		}

		params := &protocol.PublishDiagnosticsParams{
			URI:         file,
			Diagnostics: res,
		}
		slog.Debug("file diagnostics", "file", file, "diagnostics", res)

		err = s.client.PublishDiagnostics(ctx, params)
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}
