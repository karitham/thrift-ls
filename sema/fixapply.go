package sema

import (
	cmpstd "cmp"
	"context"
	"fmt"
	"slices"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

// maxFixPasses bounds FixAll's fixpoint loop. One pass may unlock another
// (adding an include resolves an undefined type, whose fix may unlock a
// third); ten passes mean the fixes oscillate, which is a fixer bug.
const maxFixPasses = 10

// FixesForFile returns every fix the pipeline offers for file: the inline
// fixes of the report's diagnostics for it, then the fixers' output for
// each diagnostic. Fixes are edits in parser coordinates on the file's
// current content.
func (p *Pipeline) FixesForFile(ctx context.Context, view *cache.View, file uri.URI, report Report) []Fix {
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

// Apply returns content with the fixes applied. A fix is all-or-nothing:
// when any of its edits overlaps an accepted fix's edits — or its own —
// the whole fix is skipped. Insertions (empty spans) never conflict with
// each other; at the same offset they land in argument order, and just
// before a replacement starting there. applied and skipped partition fixes
// in argument order. An edit outside content is an error: offsets come
// from the same parse the fixes were computed on, so a out-of-range span
// is a fixer bug, not a condition to skip over.
func Apply(content []byte, fixes []Fix) (out []byte, applied, skipped []Fix, err error) {
	type edit struct {
		start, end int
		text       string
	}

	edits := make([][]edit, len(fixes))
	accepted := make([]bool, len(fixes))

	for i, fx := range fixes {
		es := make([]edit, 0, len(fx.Edits))

		ok := true

		for _, e := range fx.Edits {
			start, end := e.Span.Start.Offset, e.Span.End.Offset
			if start < 0 || start > end || end > len(content) {
				return nil, nil, nil, fmt.Errorf("fix %q: edit range [%d:%d) outside %d bytes of content", fx.Title, start, end, len(content))
			}

			es = append(es, edit{start, end, e.NewText})
		}

		slices.SortStableFunc(es, func(a, b edit) int { return cmpstd.Compare(a.start, b.start) })

		for j := 1; j < len(es) && ok; j++ {
			if es[j-1].end > es[j].start {
				skipped = append(skipped, fx)
				ok = false
			}
		}

		if !ok {
			continue
		}

		edits[i] = es
	}

	// Accept in argument order: the first fix asking for a region wins.
	taken := make([]edit, 0, len(fixes))

	for i, fx := range fixes {
		if edits[i] == nil {
			continue
		}

		if slices.ContainsFunc(taken, func(t edit) bool {
			return slices.ContainsFunc(edits[i], func(e edit) bool {
				return e.start < t.end && t.start < e.end
			})
		}) {
			skipped = append(skipped, fx)

			continue
		}

		accepted[i] = true
		taken = append(taken, edits[i]...)
	}

	// Ascending by start, zero-length first on ties, then splice back to
	// front: the reverse walk keeps earlier offsets valid, and the tie
	// order lands a same-offset insertion's text just before a
	// replacement starting there — both effects survive, whatever the
	// argument order.
	var flat []edit

	for i, fx := range fixes {
		if accepted[i] {
			flat = append(flat, edits[i]...)
			applied = append(applied, fx)
		}
	}

	slices.SortStableFunc(flat, func(a, b edit) int {
		if c := cmpstd.Compare(a.start, b.start); c != 0 {
			return c
		}

		// Zero-length edits sort before a replacement starting at the
		// same offset; equal kinds keep argument order.
		aZero, bZero := a.start == a.end, b.start == b.end

		switch {
		case aZero && !bZero:
			return -1
		case bZero && !aZero:
			return 1
		}

		return 0
	})

	out = content

	for i := len(flat) - 1; i >= 0; i-- {
		e := flat[i]

		buf := make([]byte, 0, len(out)-(e.end-e.start)+len(e.text))
		buf = append(buf, out[:e.start]...)
		buf = append(buf, e.text...)
		buf = append(buf, out[e.end:]...)

		out = buf
	}

	return out, applied, skipped, nil
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
func (p *Pipeline) FixAll(ctx context.Context, view *cache.View, targets []uri.URI, persist func(context.Context, uri.URI, []byte) error) (FixResult, error) {
	var res FixResult

	targets = slices.Clone(targets)
	slices.Sort(targets)

	changed := make(map[uri.URI]struct{}, len(targets))

	for pass := 0; pass < maxFixPasses; pass++ {
		res.Passes++

		report, err := p.Run(ctx, view, targets)
		if err != nil {
			return res, err
		}

		res.Remaining = report

		changes := make([]*cache.FileChange, 0, len(targets))
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
				return res, err
			}

			out, ok, skip, err := Apply(content, fixes)
			if err != nil {
				return res, fmt.Errorf("%s: %w", u.FsPath(), err)
			}

			for _, fx := range skip {
				passSkipped = append(passSkipped, SkippedFix{File: u, Fix: fx, Reason: "overlaps another fix"})
			}

			if len(ok) == 0 {
				continue
			}

			applied += len(ok)

			if persist != nil {
				if err := persist(ctx, u, out); err != nil {
					return res, fmt.Errorf("%s: %w", u.FsPath(), err)
				}
			}

			changes = append(changes, &cache.FileChange{URI: u, Version: pass + 1, Content: out, From: cache.FileChangeTypeDidChange})
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

		res.Applied += applied

		view.Update(ctx, changes...)
	}

	res.FixedFiles = make([]uri.URI, 0, len(changed))
	for u := range changed {
		res.FixedFiles = append(res.FixedFiles, u)
	}

	slices.Sort(res.FixedFiles)

	return res, nil
}
