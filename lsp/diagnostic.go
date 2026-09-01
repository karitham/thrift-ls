package lsp

import (
	"context"
	"errors"
	"log/slog"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/source"
	"github.com/karitham/thrift-ls/options"
	"github.com/karitham/thrift-ls/sema"
)

// lintConfig returns the analysis pipeline config for a view's folder:
// the folder's thrift-ls.json lint settings overlaid by the workspace
// settings. New severities apply from the next analysis run.
func (s *Server) lintConfig(view *cache.View) sema.Config {
	s.optsMu.RLock()
	overlay := s.workspaceOverlay
	s.optsMu.RUnlock()

	cfg := overlay.Apply(s.folderConfig(view.Folder()))
	if cfg.Lint != nil {
		return lintConfigOf(*cfg.Lint)
	}

	return sema.Config{}
}

// lintConfigOf converts the options layer's lint settings into the
// pipeline's config. The options package stays plain data — importing
// sema from there closes a dependency cycle — so the conversion goes
// through sema.ConfigFromLint.
func lintConfigOf(l options.LintConfig) sema.Config {
	var severity map[string]string
	if l.Severity != nil {
		severity = *l.Severity
	}

	var disabled []string
	if l.Disabled != nil {
		disabled = *l.Disabled
	}

	return sema.ConfigFromLint(disabled, severity)
}

// diagnose runs the analysis pipeline once over every affected file — one
// run, one shared cross-file index — and publishes the findings per file.
// The per-file findings are cached for code actions.
func (s *Server) diagnose(ctx context.Context, view *cache.View, affected []uri.URI) {
	slog.Debug("diagnose called", "files", len(affected))
	defer slog.Debug("diagnose finished")

	report, err := sema.DefaultPipeline(s.lintConfig(view)).Run(ctx, view, affected)
	if err != nil {
		logError("diagnostic failed", err)
	}

	// Cache the findings whether or not a client is attached: code
	// actions pair fixes with the server's own diagnostics. A file that
	// no longer parses (deleted or unreadable) gets its cached report
	// dropped instead, so the cache never pins stale findings.
	s.reportMu.Lock()
	for _, file := range affected {
		if _, err := view.Parse(ctx, file); err != nil {
			delete(s.reports, file)

			continue
		}

		s.reports[file] = report
	}
	s.reportMu.Unlock()

	if s.client == nil {
		return
	}

	var errs []error

	for _, file := range affected {
		if _, err := view.Parse(ctx, file); err != nil {
			continue
		}

		diags := report[file]
		if diags == nil {
			diags = []sema.Diagnostic{}
		}

		res, err := source.ToProtocolDiagnostics(ctx, view, file, diags)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		slog.Debug("publish diagnostics", "file", file, "count", len(res))

		err = s.client.PublishDiagnostics(ctx, &protocol.PublishDiagnosticsParams{
			URI:         file,
			Diagnostics: res,
		})
		if err != nil {
			errs = append(errs, err)
		}
	}

	if err := errors.Join(errs...); err != nil {
		logError("publish failed", err)
	}
}

// reportFor returns the diagnostics last published for file.
func (s *Server) reportFor(file uri.URI) sema.Report {
	s.reportMu.RLock()
	defer s.reportMu.RUnlock()

	return s.reports[file]
}

// forgetReport drops the cached report of a file that was closed or
// deleted from disk, so the reports map never pins stale findings.
func (s *Server) forgetReport(file uri.URI) {
	s.reportMu.Lock()
	delete(s.reports, file)
	s.reportMu.Unlock()
}
