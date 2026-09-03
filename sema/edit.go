package sema

import (
	cmpstd "cmp"
	"fmt"
	"slices"
)

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
