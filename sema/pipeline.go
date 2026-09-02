package sema

import (
	"context"
	"errors"
	"log/slog"
	"slices"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
)

// File is one file's analysis inputs: its parsed tree plus the run's
// shared state.
type File struct {
	URI uri.URI
	PF  *cache.ParsedFile
	run *Run
}

// View returns the run's view (include resolver, dependency graph).
func (f File) View() *cache.View {
	return f.run.view
}

// Index returns the run's shared cross-file resolver, memoized across
// every analyzer in the run.
func (f File) Index() *Index {
	return f.run.index()
}

// Analyzer is a whole-run check: its findings may span files (include
// cycles are the current consumer), and it may need to see files that do
// not parse (Parse is the current consumer).
type Analyzer interface {
	Name() string
	Analyze(ctx context.Context, run *Run) error
}

// FileAnalyzer is the per-file shape most checks take: findings depend
// only on the file itself. The runner loops files, skips unparseable ones,
// and collects read errors; the analyzer never sees the loop.
type FileAnalyzer interface {
	Name() string
	AnalyzeFile(ctx context.Context, f File) ([]Diagnostic, error)
}

// EachFile adapts a FileAnalyzer to Analyzer.
func EachFile(a FileAnalyzer) Analyzer {
	return fileAnalyzer{a}
}

type fileAnalyzer struct{ a FileAnalyzer }

func (fa fileAnalyzer) Name() string { return fa.a.Name() }

func (fa fileAnalyzer) Analyze(ctx context.Context, run *Run) error {
	var errs []error

	for _, file := range run.files {
		pf, err := run.view.Parse(ctx, file)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		if pf.AST() == nil {
			// The file does not parse; the Parse analyzer reports that.
			// Others skip it — the partial AST may not hold their
			// invariants.
			slog.Debug("analyzer skipped: file does not parse", "analyzer", fa.Name(), "file", file)

			continue
		}

		diags, err := fa.a.AnalyzeFile(ctx, File{URI: file, PF: pf, run: run})
		if err != nil {
			errs = append(errs, err)

			continue
		}

		run.Add(file, diags...)
	}

	return errors.Join(errs...)
}

// Run is one analysis pass. Analyzers add findings to it.
type Run struct {
	view   *cache.View
	files  []uri.URI
	ix     *Index
	report Report
	cfg    Config
}

// Files returns the files the run analyzes.
func (r *Run) Files() []uri.URI {
	return r.files
}

// Add appends findings for file, applying the run's severity overrides.
// The findings are copied: the caller keeps ownership of its slice.
func (r *Run) Add(file uri.URI, ds ...Diagnostic) {
	ds = slices.Clone(ds)
	if ds == nil {
		ds = []Diagnostic{}
	}

	for i, d := range ds {
		if sev, ok := r.cfg.Severity[d.Code]; ok {
			ds[i].Severity = sev
		}
	}

	r.report[file] = append(r.report[file], ds...)
}

func (r *Run) index() *Index {
	if r.ix == nil {
		r.ix = NewIndex(r.view)
	}

	return r.ix
}

// ConfigFromLint builds a Config from the config layer's lint settings:
// the analyzer names to skip and the severity overrides by code, with
// severities named "error", "warning", "info", and "hint". Unknown names
// are ignored; the config sources validate them.
func ConfigFromLint(disabled []string, severity map[string]string) Config {
	cfg := Config{}

	if disabled != nil {
		cfg.Disabled = disabled
	}

	if severity != nil {
		cfg.Severity = make(map[string]Severity, len(severity))
		for code, name := range severity {
			if sev, ok := lintSeverityNames[name]; ok {
				cfg.Severity[code] = sev
			}
		}
	}

	return cfg
}

var lintSeverityNames = map[string]Severity{
	"error":   SeverityError,
	"warning": SeverityWarning,
	"info":    SeverityInfo,
	"hint":    SeverityHint,
}

// Config selects and tunes analyzers.
type Config struct {
	// Disabled names analyzers (by Name) to skip. nil runs all.
	Disabled []string
	// Severity overrides a diagnostic's severity by code.
	Severity map[string]Severity
}

func (c Config) disabled(name string) bool {
	return slices.Contains(c.Disabled, name)
}

// Fixer computes fixes for diagnostics reported by other analyzers, on
// demand — for fixes too expensive to compute during analysis (a workspace
// search per unresolved type, for instance). The fixer self-filters on the
// diagnostic's code and returns nothing when it has none.
type Fixer interface {
	Fix(ctx context.Context, f File, d Diagnostic) []Fix
}

// ActionProvider offers source edits for a selection, independent of any
// diagnostic: refactors. The report is available for providers whose
// actions double as quickfixes for diagnostics overlapping the selection.
type ActionProvider interface {
	Actions(ctx context.Context, f File, span Span, report Report) []Action
}

// Pipeline runs analyzers over changed files. It is a value: safe to
// share, no state beyond the analyzers, fixers, providers, and config.
type Pipeline struct {
	analyzers []Analyzer
	fixers    []Fixer
	providers []ActionProvider
	cfg       Config
}

// New composes a pipeline. Composition lives at the caller: no global
// registry.
func New(cfg Config, analyzers []Analyzer) *Pipeline {
	return &Pipeline{analyzers: analyzers, cfg: cfg}
}

// WithAnalyzers returns a copy of the pipeline with analyzers appended.
func (p *Pipeline) WithAnalyzers(analyzers ...Analyzer) *Pipeline {
	out := *p
	out.analyzers = append(slices.Clone(p.analyzers), analyzers...)

	return &out
}

// WithFixers returns a copy of the pipeline with the fixers added.
func (p *Pipeline) WithFixers(fs ...Fixer) *Pipeline {
	out := *p
	out.fixers = append(append([]Fixer{}, p.fixers...), fs...)

	return &out
}

// WithProviders returns a copy of the pipeline with the action providers
// added.
func (p *Pipeline) WithProviders(ps ...ActionProvider) *Pipeline {
	out := *p
	out.providers = append(append([]ActionProvider{}, p.providers...), ps...)

	return &out
}

// DefaultPipeline composes the built-in analyzers with their fixers and
// action providers.
func DefaultPipeline(cfg Config) *Pipeline {
	return New(cfg, Defaults()).
		WithFixers(AddIncludeFixer{}).
		WithProviders(EnumValuesProvider{}, FieldQualifierProvider{})
}

// Defaults returns the built-in analyzers.
func Defaults() []Analyzer {
	return []Analyzer{
		&CycleCheck{},
		&ParseCheck{},
		EachFile(&FieldIDCheck{}),
		EachFile(&DuplicateCheck{}),
		EachFile(&EnumValueCheck{}),
		EachFile(&UnusedIncludeCheck{}),
		EachFile(&IncludeShadowCheck{}),
		EachFile(&SemanticAnalysis{}),
		EachFile(&NonScalarMapKeyCheck{}),
	}
}

// Run analyzes changed over view: one run, one shared Index across all
// changed files and all analyzers.
func (p *Pipeline) Run(ctx context.Context, view *cache.View, changed []uri.URI) (Report, error) {
	run := &Run{view: view, files: changed, report: Report{}, cfg: p.cfg}

	var errs []error

	for _, a := range p.analyzers {
		if p.cfg.disabled(a.Name()) {
			continue
		}

		if err := a.Analyze(ctx, run); err != nil {
			errs = append(errs, err)
		}
	}

	return run.report, errors.Join(errs...)
}

// CodeActions returns the actions for span in file: quickfixes from the
// report's diagnostics overlapping span (inline fixes first, then the
// fixers), then the action providers' refactors.
func (p *Pipeline) CodeActions(ctx context.Context, view *cache.View, file uri.URI, span Span, report Report) []Action {
	pf, err := view.Parse(ctx, file)
	if err != nil || pf.AST() == nil {
		return nil
	}

	f := File{URI: file, PF: pf, run: &Run{view: view, cfg: p.cfg, report: report}}

	var out []Action

	for _, d := range report[file] {
		if !d.Span.Overlaps(span) {
			continue
		}

		for _, fix := range d.Fixes {
			out = append(out, Action{Title: fix.Title, Fix: true, File: file, Edits: fix.Edits})
		}

		for _, fx := range p.fixers {
			for _, fix := range fx.Fix(ctx, f, d) {
				out = append(out, Action{Title: fix.Title, Fix: true, File: file, Edits: fix.Edits})
			}
		}
	}

	for _, ap := range p.providers {
		out = append(out, ap.Actions(ctx, f, span, report)...)
	}

	return out
}
