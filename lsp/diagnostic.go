package lsp

import (
	"context"
	"errors"
	"log/slog"
	"slices"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/source"
	"github.com/karitham/thrift-ls/options"
	"github.com/karitham/thrift-ls/sema"
	"github.com/karitham/thrift-ls/store"
)

// lintConfig returns the analysis pipeline config for a view's folder:
// the folder's thrift-ls.json lint settings overlaid by the workspace
// settings. New severities apply from the next analysis run.
func (s *Server) lintConfig(view *store.View) sema.Config {
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

func (s *Server) pipeline(view *store.View) *sema.Pipeline {
	return sema.DefaultPipeline(s.lintConfig(view)).
		WithAnalyzers(s.analysis.Analyzers...).
		WithFixers(s.analysis.Fixers...).
		WithProviders(s.analysis.Providers...)
}

// diagnose runs the analysis pipeline once over every affected file — one
// run, one shared cross-file index — and publishes the findings per file.
// The per-file findings are cached for code actions.
func (s *Server) diagnose(ctx context.Context, view *store.View, affected []uri.URI) {
	s.diagnoseAt(ctx, view, affected, view.Generation())
}

func (s *Server) diagnoseAt(ctx context.Context, view *store.View, affected []uri.URI, generation uint64) {
	s.analysisMu.Lock()
	defer s.analysisMu.Unlock()
	ctx = store.WithGeneration(ctx, generation)

	slog.Debug("diagnose called", "files", len(affected))
	defer slog.Debug("diagnose finished")

	if !view.IsCurrent(generation) {
		return
	}

	report, err := s.pipeline(view).Run(ctx, view, affected)
	if err != nil {
		logError("diagnostic failed", err)
	}

	if !view.IsCurrent(generation) {
		return
	}

	// Cache the findings whether or not a client is attached: code
	// actions pair fixes with the server's own diagnostics. A file that
	// no longer parses (deleted or unreadable) gets its cached report
	// dropped instead, so the cache never pins stale findings.
	s.reportMu.Lock()
	defer s.reportMu.Unlock()
	if !view.IsCurrent(generation) {
		return
	}

	for _, file := range affected {
		if _, err := view.Parse(ctx, file); err != nil {
			delete(s.reports, file)

			continue
		}

		s.reports[file] = report
	}

	if s.client == nil {
		return
	}

	if !view.IsCurrent(generation) {
		return
	}

	var errs []error

	for _, file := range affected {
		if !view.IsCurrent(generation) {
			return
		}

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

		if !view.IsCurrent(generation) {
			return
		}

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

func (s *Server) clearDiagnostics(ctx context.Context, files ...uri.URI) {
	slices.Sort(files)
	files = slices.Compact(files)
	ctx = context.WithoutCancel(ctx)
	s.reportMu.Lock()
	defer s.reportMu.Unlock()

	for _, file := range files {
		delete(s.reports, file)

		if s.client == nil {
			continue
		}

		if err := s.client.PublishDiagnostics(ctx, &protocol.PublishDiagnosticsParams{
			URI:         file,
			Diagnostics: []protocol.Diagnostic{},
		}); err != nil {
			logError("clear diagnostics failed", err, "uri", file)
		}
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
