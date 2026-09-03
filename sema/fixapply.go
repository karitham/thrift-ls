package sema

import (
	"context"
	"fmt"
	"slices"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/syntax"
	"github.com/karitham/thrift-ls/vfs"
)

// maxFixPasses bounds FixAll's fixpoint loop. One pass may unlock another
// (adding an include resolves an undefined type, whose fix may unlock a
// third); ten passes mean the fixes oscillate, which is a fixer bug.
const maxFixPasses = 10

// FixesForFile returns every fix the pipeline offers for file: the inline
// fixes of the report's diagnostics for it, then the fixers' output for
// each diagnostic. Fixes are edits in parser coordinates on the file's
// current content.
func (p *Pipeline) FixesForFile(ctx context.Context, view Graph, file uri.URI, report Report) []Fix {
	pf, err := view.Parse(ctx, file)
	if err != nil || pf.AST() == nil {
		return nil
	}

	f := File{URI: file, PF: pf, run: &Run{view: view, cfg: p.cfg, report: report}}

	return p.fixesFor(ctx, f, report[file])
}

// fixesFor collects the fixes for one file's diagnostics: inline fixes
// first, then the fixers' output per diagnostic, in diagnostic order.
func (p *Pipeline) fixesFor(ctx context.Context, f File, ds []Diagnostic) []Fix {
	var out []Fix

	for _, d := range ds {
		out = append(out, d.Fixes...)

		for _, fx := range p.fixers {
			out = append(out, fx.Fix(ctx, f, d)...)
		}
	}

	return out
}

// SkippedFix is a fix FixAll could not apply.
type SkippedFix struct {
	File   uri.URI
	Fix    Fix
	Reason string
}

// FixResult reports one FixAll run.
type FixResult struct {
	// Applied is the number of fixes applied across all passes.
	Applied int
	// FixedFiles lists the files whose content changed, sorted by URI.
	FixedFiles []uri.URI
	// Skipped lists the fixes that could not apply.
	Skipped []SkippedFix
	// Passes is the number of pipeline runs.
	Passes int
	// Remaining is the last pass's report: the diagnostics left after the
	// final pass, fixable or not.
	Remaining Report
}

// FixAll runs the pipeline over targets and applies every applicable fix,
// re-running until a pass applies no fix or maxFixPasses passes have run.
// Only targets are analyzed and fixed; resolution and fixers read the
// whole view, so fixing one file of a workspace resolves against all of it
// and never touches the other files.
//
// persist receives each changed file's new content: it must make the
// content durable (write to disk, update the editor overlay) and visible
// to view's file source before returning. FixAll drives view.Update
// itself, so persist must not.
func (p *Pipeline) FixAll(ctx context.Context, view Store, targets []uri.URI, persist func(context.Context, uri.URI, []byte) error) (FixResult, error) {
	var res FixResult

	targets = slices.Clone(targets)
	slices.Sort(targets)

	changed := make(map[uri.URI]struct{}, len(targets))

	// summary assembles the result on every exit path: a persist failure
	// may leave earlier files of the same pass already mutated, and the
	// caller must be able to report that.
	summary := func() FixResult {
		res.FixedFiles = make([]uri.URI, 0, len(changed))
		for u := range changed {
			res.FixedFiles = append(res.FixedFiles, u)
		}

		slices.Sort(res.FixedFiles)

		return res
	}

	for pass := 0; pass < maxFixPasses; pass++ {
		res.Passes++

		report, err := p.Run(ctx, view, targets)
		if err != nil {
			return summary(), err
		}

		res.Remaining = report

		changes := make([]*vfs.FileChange, 0, len(targets))
		passSkipped := make([]SkippedFix, 0, len(res.Skipped))
		applied := 0

		for _, u := range targets {
			pf, err := view.Parse(ctx, u)
			if err != nil {
				continue
			}

			// A partial AST's spans are content-derived, but a
			// mis-attributed node could delete the wrong line: batch
			// fixing never touches files whose parse reported errors.
			// Warning-only parse output parses completely and fixes on.
			// Their diagnostics stay in Remaining.
			if slices.ContainsFunc(pf.Errors(), func(e syntax.Error) bool {
				return e.Severity == syntax.SeverityError
			}) {
				for _, d := range report[u] {
					for _, fx := range d.Fixes {
						passSkipped = append(passSkipped, SkippedFix{File: u, Fix: fx, Reason: "file has parse errors"})
					}
				}

				continue
			}

			fixes := p.FixesForFile(ctx, view, u, report)
			if len(fixes) == 0 {
				continue
			}

			content, err := pf.Content()
			if err != nil {
				return summary(), err
			}

			out, ok, skip, err := Apply(content, fixes)
			if err != nil {
				return summary(), fmt.Errorf("%s: %w", u.FsPath(), err)
			}

			for _, fx := range skip {
				passSkipped = append(passSkipped, SkippedFix{File: u, Fix: fx, Reason: "overlaps another fix"})
			}

			if len(ok) == 0 {
				continue
			}

			// Count and record only after the content is durable: a
			// failed persist leaves the file unmutated on disk, and the
			// summary must report what actually landed.
			if persist != nil {
				if err := persist(ctx, u, out); err != nil {
					return summary(), fmt.Errorf("%s: %w", u.FsPath(), err)
				}
			}

			applied += len(ok)
			res.Applied += len(ok)

			changes = append(changes, &vfs.FileChange{URI: u, Version: pass + 1, Content: out, From: vfs.FileChangeTypeDidChange})
			changed[u] = struct{}{}
		}

		// Only the final pass's skips are reported: earlier passes'
		// skips were recomputed against fresh ranges the next pass.
		if applied == 0 || pass == maxFixPasses-1 {
			res.Skipped = passSkipped
		}

		if applied == 0 {
			break
		}

		view.Update(ctx, changes...)
	}

	return summary(), nil
}
