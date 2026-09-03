// Package check runs the language server's diagnostic pipeline over files
// without an editor: the high-level boundary for non-LSP frontends.
// Config discovery belongs to the caller — Run takes resolved inputs —
// and so does presentation: results are data, printing is the CLI's job.
package check

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/options"
	"github.com/karitham/thrift-ls/sema"
	"github.com/karitham/thrift-ls/syntax"
)

// Request is one check run over materialized files.
type Request struct {
	// Files are the absolute thrift paths to check.
	Files []string
	// Folder is the absolute resolution root. Empty derives from the
	// first file's directory.
	Folder string
	// IncludePaths are the compiler-equivalent include roots, authoritative
	// for the run.
	IncludePaths []string
	// Lint tunes the pipeline. Nil means defaults.
	Lint *options.LintConfig
	// Fix rewrites files in place until nothing applies, preserving file
	// permissions.
	Fix bool
}

// Severity is a diagnostic's display weight.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
	SeverityHint    Severity = "hint"
)

// Diagnostic is one finding in 1-based file coordinates with UTF-16
// columns: ready to print, with no editor-protocol vocabulary.
type Diagnostic struct {
	Line, Col       int
	EndLine, EndCol int
	Severity        Severity
	Code            string
	Message         string
}

// SkippedFix is a fix that could not apply.
type SkippedFix struct {
	// File is the absolute path of the file carrying the fix.
	File string
	// Title names the fix.
	Title string
	// Reason explains why it was skipped.
	Reason string
}

// FixSummary reports a fix run.
type FixSummary struct {
	// Applied is the number of fixes applied across all passes.
	Applied int
	// Files lists the fixed files' absolute paths.
	Files []string
	// Passes is the number of pipeline runs.
	Passes int
	// Skipped lists the fixes that could not apply.
	Skipped []SkippedFix
}

// Result is one check run. Diagnostics are keyed by absolute path.
// Without Fix they hold everything found; with Fix they hold what remains
// unfixed. Fix is nil unless Request.Fix.
type Result struct {
	Diagnostics map[string][]Diagnostic
	Fix         *FixSummary
}

// Run checks req.Files through the same cache and checker pipeline the LSP
// uses, and returns the diagnostics per file. It exits no process and
// prints nothing; severity gating (failing CI on errors) belongs to the
// caller.
func Run(ctx context.Context, req Request) (Result, error) {
	folder := req.Folder
	if folder == "" {
		if len(req.Files) == 0 {
			return Result{Diagnostics: map[string][]Diagnostic{}}, nil
		}

		folder = filepath.Dir(req.Files[0])
	}

	lint := sema.Config{}
	if req.Lint != nil {
		var disabled []string
		if req.Lint.Disabled != nil {
			disabled = *req.Lint.Disabled
		}

		var severity map[string]string
		if req.Lint.Severity != nil {
			severity = *req.Lint.Severity
		}

		lint = sema.ConfigFromLint(disabled, severity)
	}

	if req.Fix {
		return runFix(ctx, req.Files, folder, req.IncludePaths, lint)
	}

	diags, err := runDiagnostics(ctx, req.Files, folder, req.IncludePaths, lint)
	if err != nil {
		return Result{}, err
	}

	return Result{Diagnostics: diags}, nil
}

// runDiagnostics runs the language server's diagnostic pipeline — parse,
// semantic analysis, and lints — over files opened in a session rooted at
// folder, and returns the diagnostics per file, keyed by absolute path.
func runDiagnostics(ctx context.Context, files []string, folder string, includePaths []string, lint sema.Config) (map[string][]Diagnostic, error) {
	_, view, uris, err := openSession(ctx, files, folder, includePaths)
	if err != nil {
		return nil, err
	}

	out := make(map[string][]Diagnostic, len(files))

	// One pipeline run over the whole corpus: the shared index memoizes
	// resolutions across files, so each name resolves once.
	report, err := sema.DefaultPipeline(lint).Run(ctx, view, uris)
	if err != nil {
		return nil, err
	}

	for i := range files {
		diags, err := toDiagnostics(ctx, view, uris[i], report[uris[i]])
		if err != nil {
			return nil, err
		}

		out[files[i]] = diags
	}

	return out, nil
}

// runFix applies the diagnostics' fixes to the checked files and reports
// what remains. Only the requested files are fixed — one file, or one
// folder — while resolution reads the whole view, so fixing a greenfield
// module resolves its types against the tree without touching the tree.
func runFix(ctx context.Context, files []string, folder string, includePaths []string, lint sema.Config) (Result, error) {
	sess, view, uris, err := openSession(ctx, files, folder, includePaths)
	if err != nil {
		return Result{}, err
	}

	// Fix passes land within the same mtime tick the memoized disk source
	// may have cached, so the fixed content flows back through the
	// session overlay: the next pass always re-parses what was written.
	version := 0

	persist := func(ctx context.Context, u uri.URI, content []byte) error {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("check canceled: %w", err)
		}

		version++

		if err := sess.UpdateOverlayFS(ctx, []*cache.FileChange{
			{URI: u, Version: version, Content: content, From: cache.FileChangeTypeDidChange},
		}); err != nil {
			return err
		}

		perms := os.FileMode(0o644)
		if info, statErr := os.Stat(u.FsPath()); statErr == nil {
			perms = info.Mode()
		}

		return os.WriteFile(u.FsPath(), content, perms)
	}

	res, err := sema.DefaultPipeline(lint).FixAll(ctx, view, uris, persist)

	summary := &FixSummary{
		Applied: res.Applied,
		Passes:  res.Passes,
		Files:   make([]string, 0, len(res.FixedFiles)),
		Skipped: make([]SkippedFix, 0, len(res.Skipped)),
	}

	for _, u := range res.FixedFiles {
		summary.Files = append(summary.Files, u.FsPath())
	}

	for _, s := range res.Skipped {
		summary.Skipped = append(summary.Skipped, SkippedFix{
			File:   s.File.FsPath(),
			Title:  s.Fix.Title,
			Reason: s.Reason,
		})
	}

	if err != nil {
		if res.Applied == 0 && len(res.FixedFiles) == 0 {
			return Result{}, err
		}

		return Result{Fix: summary}, err
	}

	remaining := make(map[string][]Diagnostic, len(files))

	for i := range files {
		diags, err := toDiagnostics(ctx, view, uris[i], res.Remaining[uris[i]])
		if err != nil {
			return Result{}, err
		}

		remaining[files[i]] = diags
	}

	return Result{Diagnostics: remaining, Fix: summary}, nil
}

// toDiagnostics translates one file's pipeline findings into printable
// diagnostics: spans map through the file's mapper to 1-based UTF-16
// columns.
func toDiagnostics(ctx context.Context, view *cache.View, file uri.URI, diags []sema.Diagnostic) ([]Diagnostic, error) {
	pf, err := view.Parse(ctx, file)
	if err != nil {
		return nil, err
	}

	out := make([]Diagnostic, len(diags))
	for i, d := range diags {
		line, col := position(pf, d.Span.Start)
		endLine, endCol := position(pf, d.Span.End)
		out[i] = Diagnostic{
			Line:     line,
			Col:      col,
			EndLine:  endLine,
			EndCol:   endCol,
			Severity: toSeverity(d.Severity),
			Code:     d.Code,
			Message:  d.Message,
		}
	}

	return out, nil
}

// position maps a parser offset to 1-based file coordinates with UTF-16
// columns. When the offset does not map, the parser's own 1-based
// coordinates pass through.
func position(pf *cache.ParsedFile, pos syntax.Position) (line, col int) {
	p, err := pf.Mapper().OffsetToLSPPosition(pos.Offset)
	if err != nil {
		return pos.Line, pos.Col
	}

	return int(p.Line) + 1, int(p.Character) + 1
}

// toSeverity maps the pipeline scale onto the display scale.
func toSeverity(s sema.Severity) Severity {
	switch s {
	case sema.SeverityError:
		return SeverityError
	case sema.SeverityWarning:
		return SeverityWarning
	case sema.SeverityInfo:
		return SeverityInfo
	case sema.SeverityHint:
		return SeverityHint
	default:
		return SeverityWarning
	}
}

// openSession opens a session with the files open in the overlay of
// a view rooted at folder, and returns the session (needed to push new
// content into the overlay later), its view, and the files' URIs.
func openSession(ctx context.Context, files []string, folder string, includePaths []string) (*cache.Session, *cache.View, []uri.URI, error) {
	sess := cache.NewSession(cache.NewMemoizedFS())
	view := sess.AddView(uri.File(folder), includePaths)

	changes := make([]*cache.FileChange, 0, len(files))
	uris := make([]uri.URI, 0, len(files))

	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			return nil, nil, nil, err
		}

		u := uri.File(file)
		uris = append(uris, u)
		changes = append(changes, &cache.FileChange{URI: u, Version: 0, Content: content, From: cache.FileChangeTypeDidOpen})
	}

	if err := sess.UpdateOverlayFS(ctx, changes); err != nil {
		return nil, nil, nil, err
	}

	return sess, view, uris, nil
}
