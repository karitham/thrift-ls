package sema_test

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/analyzers"
	"github.com/karitham/thrift-ls/analyzertest"
	"github.com/karitham/thrift-ls/internal/proptest"
	"github.com/karitham/thrift-ls/sema"
	"github.com/karitham/thrift-ls/store"
	"github.com/karitham/thrift-ls/syntax"
)

// offsetSpan builds a span from byte offsets. Apply reads only the byte
// offsets, so the line and column numbers are decorative here.
func offsetSpan(start, end int) sema.Span {
	return sema.Span{
		Start: syntax.Position{Line: 1, Col: start + 1, Offset: start},
		End:   syntax.Position{Line: 1, Col: end + 1, Offset: end},
	}
}

// applyAlphabet holds the content runes the generators draw from: ASCII,
// a two-byte rune, a three-byte rune, newlines, and spaces, so spans
// straddle multibyte and line boundaries.
var applyAlphabet = []string{"a", "b", "c", "é", "世", "\n", " "}

// applyTextAlphabet holds the replacement texts: short, sometimes empty.
var applyTextAlphabet = []string{"x", "Y", "é", "\n", " "}

// drawContent generates a short document from the alphabet.
func drawContent(t *proptest.T) string {
	n := proptest.Int(t, 0, 12)

	var b strings.Builder

	for i := 0; i < n; i++ {
		b.WriteString(proptest.Pick(t, applyAlphabet))
	}

	return b.String()
}

// drawText generates a replacement text, sometimes empty.
func drawText(t *proptest.T) string {
	n := proptest.Int(t, 0, 3)

	var b strings.Builder

	for i := 0; i < n; i++ {
		b.WriteString(proptest.Pick(t, applyTextAlphabet))
	}

	return b.String()
}

// boundsStrictlyInside returns the rune boundaries strictly between lo and
// hi.
func boundsStrictlyInside(bounds []int, lo, hi int) []int {
	var out []int

	for _, b := range bounds {
		if b > lo && b < hi {
			out = append(out, b)
		}
	}

	return out
}

// drawSpan draws a span from the rune boundaries of the content.
func drawSpan(t *proptest.T, bounds []int) (int, int) {
	// bounds is never empty: it always carries 0.
	i := proptest.Int(t, 0, len(bounds)-1)
	j := proptest.Int(t, i, len(bounds)-1)

	return bounds[i], bounds[j]
}

// drawRandomEdit draws one in-range edit on rune boundaries.
func drawRandomEdit(t *proptest.T, bounds []int) sema.Edit {
	start, end := drawSpan(t, bounds)

	return sema.Edit{Span: offsetSpan(start, end), NewText: drawText(t)}
}

// pickPrevSpan returns a span used by an earlier fix, so a later fix can
// repeat it exactly or nest inside it.
func pickPrevSpan(t *proptest.T, prev []sema.Fix) (sema.Span, bool) {
	var spans []sema.Span

	for _, fx := range prev {
		for _, e := range fx.Edits {
			spans = append(spans, e.Span)
		}
	}

	if len(spans) == 0 {
		return sema.Span{}, false
	}

	return spans[proptest.Int(t, 0, len(spans)-1)], true
}

// drawFix generates one fix: a plain 1-3 edit fix, a repeat of an earlier
// fix's exact span, a span nested inside an earlier one, a same-start
// insertion+replacement pair (either declaration order), a self-overlapping
// fix, or an empty fix.
func drawFix(t *proptest.T, label string, bounds []int, prev []sema.Fix) sema.Fix {
	fx := sema.Fix{Title: label}

	shape := proptest.Pick(t, []string{
		"plain", "plain", "repeat", "nested", "same-start", "self-overlap", "empty",
	})

	switch shape {
	case "empty":
		return fx

	case "repeat":
		if span, ok := pickPrevSpan(t, prev); ok {
			fx.Edits = append(fx.Edits, sema.Edit{Span: span, NewText: drawText(t)})

			if proptest.Bool(t) {
				fx.Edits = append(fx.Edits, drawRandomEdit(t, bounds))
			}

			return fx
		}

	case "nested":
		if outer, ok := pickPrevSpan(t, prev); ok {
			if inner := boundsStrictlyInside(bounds, outer.Start.Offset, outer.End.Offset); len(inner) >= 1 {
				a := proptest.Int(t, 0, len(inner)-1)
				b := proptest.Int(t, a, len(inner)-1)

				fx.Edits = append(fx.Edits,
					sema.Edit{Span: outer, NewText: drawText(t)},
					sema.Edit{Span: offsetSpan(inner[a], inner[b]), NewText: drawText(t)},
				)

				return fx
			}
		}

	case "same-start":
		if len(bounds) >= 2 {
			i := proptest.Int(t, 0, len(bounds)-2)
			j := proptest.Int(t, i+1, len(bounds)-1)
			start, end := bounds[i], bounds[j]

			ins := sema.Edit{Span: offsetSpan(start, start), NewText: drawText(t)}
			rep := sema.Edit{Span: offsetSpan(start, end), NewText: drawText(t)}

			// Both declaration orders: the splicer's self-overlap check
			// sorts by start and keeps declaration order on ties, so the
			// two orders are not equivalent.
			if proptest.Bool(t) {
				fx.Edits = []sema.Edit{ins, rep}
			} else {
				fx.Edits = []sema.Edit{rep, ins}
			}

			return fx
		}

	case "self-overlap":
		if len(bounds) >= 3 {
			i := proptest.Int(t, 0, len(bounds)-3)
			j := proptest.Int(t, i+2, len(bounds)-1)
			start, end := bounds[i], bounds[j]
			mid := bounds[proptest.Int(t, i+1, j-1)]

			// An insertion inside a replacement overlaps it.
			fx.Edits = []sema.Edit{
				{Span: offsetSpan(start, end), NewText: drawText(t)},
				{Span: offsetSpan(mid, mid), NewText: drawText(t)},
			}

			return fx
		}
	}

	n := proptest.Int(t, 1, 3)

	for k := 0; k < n; k++ {
		fx.Edits = append(fx.Edits, drawRandomEdit(t, bounds))
	}

	return fx
}

// applyCase is one generated Apply input. Fix titles are unique within a
// case, so failures name the fixes.
type applyCase struct {
	content string
	fixes   []sema.Fix
}

func (c applyCase) String() string {
	var b strings.Builder

	fmt.Fprintf(&b, "content %q", c.content)

	for _, fx := range c.fixes {
		fmt.Fprintf(&b, "\n  %s:", fx.Title)

		for _, e := range fx.Edits {
			fmt.Fprintf(&b, " [%d:%d)->%q", e.Span.Start.Offset, e.Span.End.Offset, e.NewText)
		}
	}

	return b.String()
}

// drawApplyCase generates content plus a fix list that always contains a
// same-start insertion/replacement pair in one of the two argument orders.
func drawApplyCase(t *proptest.T) applyCase {
	content := drawContent(t)
	bounds := proptest.RuneBoundaries(content)

	var fixes []sema.Fix

	n := proptest.Int(t, 0, 4)
	for i := 0; i < n; i++ {
		fixes = append(fixes, drawFix(t, fmt.Sprintf("f%d", i), bounds, fixes))
	}

	// The tie pair: an insertion at a replacement's start offset, in either
	// argument order. The insertion's text must land before the replacement's.
	if len(bounds) >= 2 {
		i := proptest.Int(t, 0, len(bounds)-2)
		j := proptest.Int(t, i+1, len(bounds)-1)
		start, end := bounds[i], bounds[j]

		ins := sema.Fix{Title: "tie-insert", Edits: []sema.Edit{{Span: offsetSpan(start, start), NewText: drawText(t)}}}
		rep := sema.Fix{Title: "tie-replace", Edits: []sema.Edit{{Span: offsetSpan(start, end), NewText: drawText(t)}}}

		if proptest.Bool(t) {
			fixes = append([]sema.Fix{ins, rep}, fixes...)
		} else {
			fixes = append([]sema.Fix{rep, ins}, fixes...)
		}
	}

	return applyCase{content: content, fixes: fixes}
}

// modelEdit is one edit in the model's coordinates.
type modelEdit struct {
	start, end int
	text       string
}

// modelSkip is a fix the model rejected, and why.
type modelSkip struct {
	index  int
	reason string // "self": the fix's own edits overlap; "overlap": an accepted fix holds part of it
}

// modelApply is an independent implementation of the contract stated in
// sema/edit.go's Apply doc comment:
//
//   - a fix is all-or-nothing;
//   - a fix overlapping an accepted fix or itself is skipped whole;
//   - insertions (empty spans) never conflict with each other;
//   - at the same offset insertions land in argument order before a
//     replacement starting there;
//   - applied and skipped partition the fixes in argument order;
//   - an edit outside the content is an error for the whole call.
//
// It is written as a forward assembly over the accepted edits, not as
// production's reverse splice, so an agreement is not a tautology.
func modelApply(content string, fixes []sema.Fix) (string, []int, []modelSkip, error) {
	perFix := make([][]modelEdit, len(fixes))

	for i, fx := range fixes {
		eds := make([]modelEdit, 0, len(fx.Edits))

		for _, e := range fx.Edits {
			start, end := e.Span.Start.Offset, e.Span.End.Offset
			if start < 0 || start > end || end > len(content) {
				return "", nil, nil, fmt.Errorf("fix %q: edit range [%d:%d) outside %d bytes of content", fx.Title, start, end, len(content))
			}

			eds = append(eds, modelEdit{start: start, end: end, text: e.NewText})
		}

		slices.SortStableFunc(eds, func(a, b modelEdit) int { return cmp.Compare(a.start, b.start) })
		perFix[i] = eds
	}

	// A fix whose own edits overlap is skipped. After sorting by start, any
	// overlapping pair shows up as an earlier edit ending past a later
	// edit's start.
	selfOverlapping := make([]bool, len(fixes))

	for i, eds := range perFix {
		for j := 1; j < len(eds); j++ {
			for k := 0; k < j; k++ {
				if eds[k].end > eds[j].start {
					selfOverlapping[i] = true
				}
			}
		}
	}

	var (
		taken    []modelEdit
		applied  []int
		skipped  []modelSkip
		accepted = make([]bool, len(fixes))
	)

	// Accept in argument order: the first fix asking for a region wins.
	for i := range fixes {
		if selfOverlapping[i] {
			skipped = append(skipped, modelSkip{index: i, reason: "self"})

			continue
		}

		blocked := false

		for _, e := range perFix[i] {
			for _, tk := range taken {
				if e.start < tk.end && tk.start < e.end {
					blocked = true
				}
			}
		}

		if blocked {
			skipped = append(skipped, modelSkip{index: i, reason: "overlap"})

			continue
		}

		accepted[i] = true
		taken = append(taken, perFix[i]...)
	}

	// Splice forward: flatten the accepted fixes in argument order, order
	// the edits ascending by start with zero-length edits first on a tie
	// (equal kinds keep argument order), then copy the content through.
	var flat []modelEdit

	for i := range fixes {
		if accepted[i] {
			applied = append(applied, i)
			flat = append(flat, perFix[i]...)
		}
	}

	slices.SortStableFunc(flat, func(a, b modelEdit) int {
		if c := cmp.Compare(a.start, b.start); c != 0 {
			return c
		}

		aZero, bZero := a.start == a.end, b.start == b.end

		switch {
		case aZero && !bZero:
			return -1
		case bZero && !aZero:
			return 1
		}

		return 0
	})

	var out strings.Builder

	cursor := 0

	for _, e := range flat {
		if e.start < cursor {
			return "", nil, nil, fmt.Errorf("model: edit [%d:%d) starts before cursor %d", e.start, e.end, cursor)
		}

		out.WriteString(content[cursor:e.start])
		out.WriteString(e.text)

		cursor = e.end
	}

	out.WriteString(content[cursor:])

	return out.String(), applied, skipped, nil
}

// fixIndices maps the fixes production returned back to their argument
// indices.
func fixIndices(fixes []sema.Fix, subset []sema.Fix, label string) []int {
	index := make(map[string]int, len(fixes))
	for i, fx := range fixes {
		index[fx.Title] = i
	}

	out := make([]int, 0, len(subset))

	for _, fx := range subset {
		i, ok := index[fx.Title]
		if !ok {
			panic(label + ": fix " + fx.Title + " is not one of the input fixes")
		}

		out = append(out, i)
	}

	return out
}

// mixesSkipReasons reports whether the model skipped fixes for both reasons.
func mixesSkipReasons(skipped []modelSkip) bool {
	var self, overlap bool

	for _, s := range skipped {
		if s.reason == "self" {
			self = true
		} else {
			overlap = true
		}
	}

	return self && overlap
}

// applySeeds is the corpus every Apply and FixAll property in this file starts
// from. proptest.New derives its draw stream from the bytes' hash, so each entry
// starts one independent stream per target, and the fuzzer varies the bytes
// from there.
var applySeeds = proptest.Corpus{
	[]byte("abcd"),
	[]byte("aé 世\n"),
	[]byte("same-start tie"),
	[]byte("include \"dep.thrift\"\n\nstruct S {\n  1: i32 a,\n}\n"),
	[]byte("enum Color {\n  RED,\n  BLUE,\n}\n"),
	[]byte("typedef i32 Age\ntypedef Age Years\n"),
}

// FuzzApplyMatchesModel is the differential property for Apply: an
// independent splicer implementing the doc comment must produce the same
// bytes and the same applied/skipped partition as production, for every
// generated fix batch.
func FuzzApplyMatchesModel(f *testing.F) {
	proptest.Fuzz(f, applySeeds, func(t *proptest.T) {
		c := drawApplyCase(t)

		gotOut, gotApplied, gotSkipped, err := sema.Apply([]byte(c.content), c.fixes)
		wantOut, wantApplied, wantSkipped, wantErr := modelApply(c.content, c.fixes)

		require.NoError(t, wantErr, "the generator only emits in-range spans: %s", c)
		require.NoError(t, err, "case:\n%s", c)

		require.Equal(t, wantOut, string(gotOut), "output bytes must match the model\ncase:\n%s", c)

		// Applied fixes, in argument order.
		require.Equal(t, append([]int{}, wantApplied...), fixIndices(c.fixes, gotApplied, "applied"),
			"applied fixes\ncase:\n%s", c)

		// Skipped fixes partition the input with applied. This property
		// checks membership; TestApplySkippedOrderContract checks their order.
		wantSkip := make([]int, 0, len(wantSkipped))
		for _, s := range wantSkipped {
			wantSkip = append(wantSkip, s.index)
		}

		gotSkip := fixIndices(c.fixes, gotSkipped, "skipped")
		slices.Sort(gotSkip)
		require.Equal(t, wantSkip, gotSkip, "skipped fixes\ncase:\n%s", c)

		if !mixesSkipReasons(wantSkipped) {
			require.Equal(t, wantSkip, fixIndices(c.fixes, gotSkipped, "skipped"),
				"one-reason skips are reported in argument order\ncase:\n%s", c)
		}

		require.Equal(t, len(c.fixes), len(gotApplied)+len(gotSkipped), "applied and skipped partition the fixes")

		// Length arithmetic: every applied edit's replacement delta lands.
		wantLen := len(c.content)
		for _, i := range wantApplied {
			for _, e := range c.fixes[i].Edits {
				wantLen += len(e.NewText) - (e.Span.End.Offset - e.Span.Start.Offset)
			}
		}

		require.Equal(t, wantLen, len(gotOut), "output length\ncase:\n%s", c)
	})
}

// applySequentially applies edits one at a time on the current content,
// translating each edit's original offsets by the deltas already applied.
// It is well defined only for edits that do not touch: the generator
// leaves at least one rune between neighbours.
func applySequentially(t *proptest.T, content string, edits []sema.Edit) string {
	cur := content

	var done []sema.Edit

	for _, e := range edits {
		shift := 0

		for _, d := range done {
			if d.Span.End.Offset <= e.Span.Start.Offset {
				shift += len(d.NewText) - (d.Span.End.Offset - d.Span.Start.Offset)
			}
		}

		start, end := e.Span.Start.Offset+shift, e.Span.End.Offset+shift
		require.GreaterOrEqual(t, start, 0)
		require.LessOrEqual(t, end, len(cur))
		require.LessOrEqual(t, start, end)

		cur = cur[:start] + e.NewText + cur[end:]
		done = append(done, e)
	}

	return cur
}

// drawDisjointFixes draws up to n edits that leave at least one rune
// between neighbours, one fix per edit.
func drawDisjointFixes(t *proptest.T, bounds []int, n int) []sema.Fix {
	if n <= 0 || len(bounds) < 3 {
		return nil
	}

	maxIdx := len(bounds) - 1

	idx := proptest.Distinct(t, proptest.IntRange(0, maxIdx), 2, min(2*n, maxIdx+1), func(i int) int { return i })

	slices.Sort(idx)
	idx = slices.Compact(idx)

	var fixes []sema.Fix

	for i := 0; i+1 < len(idx); i += 2 {
		fixes = append(fixes, sema.Fix{
			Title: fmt.Sprintf("d%d", len(fixes)),
			Edits: []sema.Edit{{Span: offsetSpan(bounds[idx[i]], bounds[idx[i+1]]), NewText: drawText(t)}},
		})
	}

	return fixes
}

// FuzzApplyDisjointCommutation is the metamorphic relation for Apply:
// pairwise-disjoint fixes commute. Applying them as one batch, in either
// argument order, equals applying them one at a time in that order with
// offset compensation.
func FuzzApplyDisjointCommutation(f *testing.F) {
	proptest.Fuzz(f, applySeeds, func(t *proptest.T) {
		content := drawContent(t)
		bounds := proptest.RuneBoundaries(content)

		fixes := drawDisjointFixes(t, bounds, proptest.Int(t, 1, 4))
		if len(fixes) < 2 {
			return
		}

		order1 := proptest.Permute(t, fixes)
		order2 := proptest.Permute(t, fixes)

		want, _, _, err := modelApply(content, order1)
		require.NoError(t, err)

		for _, order := range [][]sema.Fix{order1, order2} {
			edits := make([]sema.Edit, 0, len(order))
			for _, fx := range order {
				edits = append(edits, fx.Edits...)
			}

			got, applied, skipped, err := sema.Apply([]byte(content), order)
			require.NoError(t, err)
			require.Len(t, applied, len(order), "disjoint fixes are all applied")
			require.Empty(t, skipped)
			require.Equal(t, want, string(got), "batch application is order independent (content %q)", content)

			sequential := applySequentially(t, content, edits)
			require.Equal(t, want, sequential, "batch equals sequential application (content %q)", content)
		}

		// The sequential form of the second order lands on the same bytes.
		edits2 := make([]sema.Edit, 0, len(order2))
		for _, fx := range order2 {
			edits2 = append(edits2, fx.Edits...)
		}

		require.Equal(t, want, applySequentially(t, content, edits2))
	})
}

// FuzzApplySameOffsetTie pins the tie rule directly: at one offset, any
// number of insertions land in argument order, immediately before a
// replacement starting there, in whatever order the fixes are passed.
func FuzzApplySameOffsetTie(f *testing.F) {
	proptest.Fuzz(f, applySeeds, func(t *proptest.T) {
		content := drawContent(t)
		bounds := proptest.RuneBoundaries(content)

		if len(bounds) < 2 {
			return
		}

		i := proptest.Int(t, 0, len(bounds)-2)
		j := proptest.Int(t, i+1, len(bounds)-1)
		start, end := bounds[i], bounds[j]

		rep := sema.Fix{Title: "replace", Edits: []sema.Edit{{Span: offsetSpan(start, end), NewText: drawText(t)}}}

		n := proptest.Int(t, 1, 3)

		var inserts []sema.Fix

		for k := 0; k < n; k++ {
			inserts = append(inserts, sema.Fix{
				Title: fmt.Sprintf("insert%d", k),
				Edits: []sema.Edit{{Span: offsetSpan(start, start), NewText: drawText(t)}},
			})
		}

		fixes := append([]sema.Fix{rep}, inserts...)
		fixes = proptest.Permute(t, fixes)

		out, applied, skipped, err := sema.Apply([]byte(content), fixes)
		require.NoError(t, err)
		require.Empty(t, skipped)
		require.Len(t, applied, len(fixes))

		// Insertion texts in argument order, then the replacement text.
		var want strings.Builder

		want.WriteString(content[:start])

		for _, fx := range fixes {
			if fx.Title == "replace" {
				continue
			}

			want.WriteString(fx.Edits[0].NewText)
		}

		want.WriteString(rep.Edits[0].NewText)
		want.WriteString(content[end:])

		require.Equal(t, want.String(), string(out), "insertions precede the replacement at their offset")
	})
}

// FuzzApplyInsertionsNeverConflict checks the documented promise that
// insertions never conflict with each other: any number of them at any
// offsets all apply, in argument order per offset.
func FuzzApplyInsertionsNeverConflict(f *testing.F) {
	proptest.Fuzz(f, applySeeds, func(t *proptest.T) {
		content := drawContent(t)
		bounds := proptest.RuneBoundaries(content)

		n := proptest.Int(t, 1, 5)

		var fixes []sema.Fix

		for k := 0; k < n; k++ {
			at := bounds[proptest.Int(t, 0, len(bounds)-1)]
			fixes = append(fixes, sema.Fix{
				Title: fmt.Sprintf("i%d", k),
				Edits: []sema.Edit{{Span: offsetSpan(at, at), NewText: drawText(t)}},
			})
		}

		out, applied, skipped, err := sema.Apply([]byte(content), fixes)
		require.NoError(t, err)
		require.Empty(t, skipped, "insertions never conflict")
		require.Len(t, applied, len(fixes))

		// Inserting equals the model's forward assembly.
		want, _, _, err := modelApply(content, fixes)
		require.NoError(t, err)
		require.Equal(t, want, string(out))
	})
}

// FuzzApplySelfOverlapSkipsWholeFix: a fix whose own edits overlap is
// skipped whole, leaving the content untouched.
func FuzzApplySelfOverlapSkipsWholeFix(f *testing.F) {
	proptest.Fuzz(f, applySeeds, func(t *proptest.T) {
		content := drawContent(t)
		bounds := proptest.RuneBoundaries(content)

		if len(bounds) < 3 {
			return
		}

		i := proptest.Int(t, 0, len(bounds)-3)
		j := proptest.Int(t, i+2, len(bounds)-1)
		start, end := bounds[i], bounds[j]
		mid := bounds[proptest.Int(t, i+1, j-1)]

		bad := sema.Fix{Title: "bad", Edits: []sema.Edit{
			{Span: offsetSpan(start, end), NewText: "Z"},
			{Span: offsetSpan(mid, mid), NewText: "I"},
		}}

		out, applied, skipped, err := sema.Apply([]byte(content), []sema.Fix{bad})
		require.NoError(t, err)
		require.Empty(t, applied)
		require.Equal(t, []string{"bad"}, fixTitles(skipped))
		require.Equal(t, content, string(out), "a self-overlapping fix changes nothing")
	})
}

// FuzzApplyRangeErrors: an out-of-range or reversed edit is an error for
// the whole call, the error names the offending fix, and no result is
// returned.
func FuzzApplyRangeErrors(f *testing.F) {
	proptest.Fuzz(f, applySeeds, func(t *proptest.T) {
		c := drawApplyCase(t)

		// Corrupt one fix in the batch: the error must veto the rest.
		mode := proptest.Pick(t, []string{"past-end", "negative", "reversed"})

		var bad sema.Fix

		switch mode {
		case "past-end":
			bad = sema.Fix{Title: "bad", Edits: []sema.Edit{{Span: offsetSpan(len(c.content), len(c.content)+1+proptest.Int(t, 0, 3)), NewText: "X"}}}
		case "negative":
			bad = sema.Fix{Title: "bad", Edits: []sema.Edit{{Span: offsetSpan(-1-proptest.Int(t, 0, 3), 0), NewText: "X"}}}
		case "reversed":
			bad = sema.Fix{Title: "bad", Edits: []sema.Edit{{Span: offsetSpan(1, 0), NewText: "X"}}}
		}

		// Anywhere in the batch: validation covers the whole call, so the
		// valid fixes before it must not run either.
		pos := proptest.Int(t, 0, len(c.fixes))
		fixes := append(append(slices.Clone(c.fixes[:pos]), bad), c.fixes[pos:]...)

		out, applied, skipped, err := sema.Apply([]byte(c.content), fixes)
		require.Error(t, err)
		require.Contains(t, err.Error(), "bad", "the error names the offending fix")
		require.Nil(t, out)
		require.Nil(t, applied)
		require.Nil(t, skipped)
	})
}

// fixTitles returns the fixes' titles, in order.
func fixTitles(fixes []sema.Fix) []string {
	out := make([]string, 0, len(fixes))

	for _, fx := range fixes {
		out = append(out, fx.Title)
	}

	return out
}

// TestApplyPinnedEdgeCases covers contract corners the doc comment does not
// spell out.
func TestApplyPinnedEdgeCases(t *testing.T) {
	for _, tt := range []struct {
		name        string
		content     string
		fixes       []sema.Fix
		want        string
		wantApplied []string
		wantSkipped []string
	}{
		{
			name:        "empty fix",
			content:     "abcd",
			fixes:       []sema.Fix{{Title: "empty"}},
			want:        "abcd",
			wantApplied: []string{"empty"},
		},
		{
			name:    "insertion before same-start replacement",
			content: "abcd",
			fixes: []sema.Fix{{
				Title: "ins-then-rep",
				Edits: []sema.Edit{
					{Span: offsetSpan(1, 1), NewText: "I"},
					{Span: offsetSpan(1, 3), NewText: "R"},
				},
			}},
			want:        "aIRd",
			wantApplied: []string{"ins-then-rep"},
		},
		{
			name:    "replacement before same-start insertion",
			content: "abcd",
			fixes: []sema.Fix{{
				Title: "rep-then-ins",
				Edits: []sema.Edit{
					{Span: offsetSpan(1, 3), NewText: "R"},
					{Span: offsetSpan(1, 1), NewText: "I"},
				},
			}},
			want:        "abcd",
			wantSkipped: []string{"rep-then-ins"},
		},
		{
			name:    "two insertions in one fix",
			content: "ab",
			fixes: []sema.Fix{{
				Title: "twice",
				Edits: []sema.Edit{
					{Span: offsetSpan(1, 1), NewText: "1"},
					{Span: offsetSpan(1, 1), NewText: "2"},
				},
			}},
			want:        "a12b",
			wantApplied: []string{"twice"},
		},
		{
			name:    "two overlapping replacements in one fix",
			content: "abcd",
			fixes: []sema.Fix{{
				Title: "twice",
				Edits: []sema.Edit{
					{Span: offsetSpan(1, 3), NewText: "1"},
					{Span: offsetSpan(1, 3), NewText: "2"},
				},
			}},
			want:        "abcd",
			wantSkipped: []string{"twice"},
		},
		{
			name:    "insertion at replacement end",
			content: "abcd",
			fixes: []sema.Fix{
				{Title: "rep", Edits: []sema.Edit{{Span: offsetSpan(1, 3), NewText: "R"}}},
				{Title: "ins", Edits: []sema.Edit{{Span: offsetSpan(3, 3), NewText: "I"}}},
			},
			want:        "aRId",
			wantApplied: []string{"rep", "ins"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out, applied, skipped, err := sema.Apply([]byte(tt.content), tt.fixes)
			require.NoError(t, err)
			require.Equal(t, tt.want, string(out))
			require.Equal(t, append([]string{}, tt.wantApplied...), fixTitles(applied))
			require.Equal(t, append([]string{}, tt.wantSkipped...), fixTitles(skipped))
		})
	}
}

// TestApplySkippedOrderContract checks that skipped fixes follow argument
// order even when a batch mixes self-overlap and conflict skips.
func TestApplySkippedOrderContract(t *testing.T) {
	fixes := []sema.Fix{
		// f0 wins [0:2).
		{Title: "f0", Edits: []sema.Edit{{Span: offsetSpan(0, 2), NewText: "X"}}},
		// f1 overlaps f0: skipped on acceptance (second loop).
		{Title: "f1", Edits: []sema.Edit{{Span: offsetSpan(1, 3), NewText: "Y"}}},
		// f2 overlaps itself: skipped on validation (first loop).
		{Title: "f2", Edits: []sema.Edit{
			{Span: offsetSpan(10, 12), NewText: "A"},
			{Span: offsetSpan(11, 13), NewText: "B"},
		}},
	}

	_, _, skipped, err := sema.Apply([]byte("abcdefghijklmnop"), fixes)
	require.NoError(t, err)

	require.Equal(t, []string{"f1", "f2"}, fixTitles(skipped),
		"skipped fixes must be reported in argument order, as Apply's doc comment states")
}

// fixPassCap mirrors sema's unexported maxFixPasses, the fixpoint loop's
// bound. Reaching it means the fixes oscillate.
const fixPassCap = 10

// persistCall is one persist invocation FixAll made.
type persistCall struct {
	file    uri.URI
	content []byte
}

// persistRecorder is a persist callback with fault injection. It records
// every call. A positive failAt fails that call (1-based) before writing
// anything, which is the contract's failure mode: the file stays
// unmutated on disk. When visible is false the content goes to a shadow
// map instead of the view's file source, so the view keeps reading the old
// content, a failure mode FixAll must survive.
type persistRecorder struct {
	source  map[uri.URI][]byte
	shadow  map[uri.URI][]byte
	calls   []persistCall
	failAt  int
	visible bool
}

func (p *persistRecorder) persist(_ context.Context, u uri.URI, content []byte) error {
	p.calls = append(p.calls, persistCall{file: u, content: slices.Clone(content)})

	if p.failAt > 0 && len(p.calls) == p.failAt {
		return fmt.Errorf("injected persist failure for %s", u.FsPath())
	}

	dst := p.source
	if !p.visible {
		if p.shadow == nil {
			p.shadow = make(map[uri.URI][]byte)
		}

		dst = p.shadow
	}

	dst[u] = slices.Clone(content)

	return nil
}

// cloneContents copies a file-source map.
func cloneContents(files map[uri.URI][]byte) map[uri.URI][]byte {
	out := make(map[uri.URI][]byte, len(files))
	for u, content := range files {
		out[u] = slices.Clone(content)
	}

	return out
}

// fixCase is one generated FixAll workspace: file contents by name, the
// sorted names, and the targets FixAll runs over.
type fixCase struct {
	files   map[string]string
	names   []string
	targets []string
}

// drawFixCase generates a workspace whose target main.thrift always
// carries at least one fixable diagnostic: an unused include, an undefined
// type with a workspace definition, or an implicit enum value.
func drawFixCase(t *proptest.T) fixCase {
	files := map[string]string{}

	// dep.thrift: the include target. The enum shape adds implicit enum
	// values, which are fixable when dep is a target.
	var depType string

	switch proptest.Int(t, 0, 1) {
	case 0:
		files["dep.thrift"] = "struct User {\n  1: i32 id,\n}\n"
		depType = "User"
	case 1:
		members := []string{"RED", "GREEN", "BLUE"}[:proptest.Int(t, 1, 3)]

		var b strings.Builder

		b.WriteString("enum Color {\n")

		for _, m := range members {
			fmt.Fprintf(&b, "  %s,\n", m)
		}

		b.WriteString("}\n")

		files["dep.thrift"] = b.String()
		depType = "Color"
	}

	// other.thrift: a second definition site, so the include fixer has to
	// choose among files.
	if proptest.Bool(t) {
		files["other.thrift"] = "struct Ghost {\n  1: i32 g,\n}\n"
	}

	include := proptest.Bool(t)
	use := proptest.Bool(t)
	useGhost := proptest.Bool(t)

	var main strings.Builder

	if include {
		main.WriteString("include \"dep.thrift\"\n")
	}

	if use {
		typeName := depType
		if useGhost && files["other.thrift"] != "" {
			typeName = "Ghost"
		}

		fmt.Fprintf(&main, "\nstruct S {\n  1: %s v,\n}\n", typeName)
	} else {
		main.WriteString("\nstruct S {\n  1: i32 a,\n}\n")
	}

	// An included-but-unused file and a used-but-unincluded file are both
	// fixable. When neither applies the file is clean, so force the third
	// fixable source, an implicit enum value.
	if include == use || proptest.Bool(t) {
		main.WriteString("\nenum E {\n  A,\n  B,\n}\n")
	}

	files["main.thrift"] = main.String()

	if proptest.Bool(t) {
		files["broken.thrift"] = "include \"dep.thrift\"\n\nstruct Broken {\n  1: i32 a\n  @@@\n}\n"
	}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}

	slices.Sort(names)

	// main.thrift is always a target: it is the fixable one by
	// construction. The rest join with a probability each.
	targets := []string{"main.thrift"}

	for _, name := range []string{"dep.thrift", "other.thrift", "broken.thrift"} {
		if _, ok := files[name]; ok && proptest.Bool(t) {
			targets = append(targets, name)
		}
	}

	slices.Sort(targets)

	return fixCase{files: files, names: names, targets: targets}
}

// newFixWorkspace builds a memFS-backed view over the case, parsed like
// analyzertest.RunFixAll does.
func newFixWorkspace(t *proptest.T, c fixCase) (*store.View, map[uri.URI][]byte) {
	t.Helper()

	contents := make(map[uri.URI][]byte, len(c.files))
	for name, content := range c.files {
		contents[analyzertest.URI(name)] = []byte(content)
	}

	view := store.NewView(uri.File("/tmp/"), store.NewMemFS(contents), nil)

	for _, name := range c.names {
		_, err := view.Parse(t.Context(), analyzertest.URI(name))
		require.NoError(t, err)
	}

	return view, contents
}

// targetURIs maps the case's target names to URIs.
func targetURIs(c fixCase) []uri.URI {
	out := make([]uri.URI, 0, len(c.targets))
	for _, name := range c.targets {
		out = append(out, analyzertest.URI(name))
	}

	return out
}

// hasParseError reports whether pf carries a severity-error parse
// diagnostic, the condition under which FixAll skips the file.
func hasParseError(pf *store.ParsedFile) bool {
	return slices.ContainsFunc(pf.Errors(), func(e syntax.Error) bool {
		return e.Severity == syntax.SeverityError
	})
}

// parseContent parses content as a lone file in a throwaway view.
func parseContent(t *proptest.T, u uri.URI, content []byte) *store.ParsedFile {
	t.Helper()

	view := store.NewView(uri.File("/tmp/"), store.NewMemFS(map[uri.URI][]byte{u: content}), nil)

	pf, err := view.Parse(t.Context(), u)
	require.NoError(t, err)

	return pf
}

// reportProjection flattens a report to comparable strings: code, byte
// span, severity, fix count, and message per diagnostic.
func reportProjection(r sema.Report) map[string][]string {
	out := make(map[string][]string, len(r))

	for u, diags := range r {
		if len(diags) == 0 {
			continue
		}

		items := make([]string, 0, len(diags))

		for _, d := range diags {
			items = append(items, fmt.Sprintf("%s %d:%d sev=%d fixes=%d %s",
				d.Code, d.Span.Start.Offset, d.Span.End.Offset, d.Severity, len(d.Fixes), d.Message))
		}

		out[u.FsPath()] = items
	}

	return out
}

// normalizeURIs makes a URI slice comparable to an empty non-nil one.
func normalizeURIs(uris []uri.URI) []uri.URI {
	return append([]uri.URI{}, uris...)
}

// FuzzFixAllConverges drives the default pipeline's fixpoint loop over
// generated workspaces: the loop terminates within the pass cap, only
// targets change, fixed files are reported, fixes never leave a file
// unparseable, and a converged run applies nothing when run again with the
// same report.
func FuzzFixAllConverges(f *testing.F) {
	proptest.Fuzz(f, applySeeds, func(t *proptest.T) {
		c := drawFixCase(t)
		pipeline := analyzers.DefaultPipeline(sema.Config{})

		view, source := newFixWorkspace(t, c)
		before := cloneContents(source)
		targets := targetURIs(c)

		rec := &persistRecorder{source: source, visible: true}

		res, err := pipeline.FixAll(t.Context(), view, targets, rec.persist)
		require.NoError(t, err)
		require.GreaterOrEqual(t, res.Passes, 1)
		require.LessOrEqual(t, res.Passes, fixPassCap, "the fixpoint loop is bounded")
		require.True(t, slices.IsSorted(res.FixedFiles), "FixedFiles is sorted")

		// Only targets are analyzed, and skips carry one of the two
		// documented reasons.
		for u := range res.Remaining {
			require.Contains(t, targets, u, "only targets are analyzed")
		}

		for _, s := range res.Skipped {
			require.Contains(t, targets, s.File)
			require.Contains(t, []string{"overlaps another fix", "file has parse errors"}, s.Reason)
		}

		// A changed file is always reported; nothing outside the targets
		// moved.
		for u, content := range source {
			if !slices.Equal(content, before[u]) {
				require.Contains(t, res.FixedFiles, u, "changed file %s must be reported", u.FsPath())
			}

			if !slices.Contains(targets, u) {
				require.Equal(t, before[u], content, "non-target %s must not be fixed", u.FsPath())
			}
		}

		// No fix leaves a file unparseable.
		for _, u := range res.FixedFiles {
			pf := parseContent(t, u, source[u])
			require.False(t, hasParseError(pf), "a fix must not break the parse of %s: %s", u.FsPath(), source[u])
		}

		// Fixpoint: a converged run applies nothing when run again, and no
		// inline fix is left that Apply alone would take. The last pass
		// applied nothing, so every fix it offered was skipped for one of
		// the two reasons (an unparseable file or a fixer whose own edits
		// overlap), never because a fix was still waiting for a pass.
		if res.Passes < fixPassCap {
			rec2 := &persistRecorder{source: source, visible: true}

			res2, err := pipeline.FixAll(t.Context(), view, targets, rec2.persist)
			require.NoError(t, err)
			require.Zero(t, res2.Applied, "a converged run must apply nothing")
			require.Equal(t, 1, res2.Passes)
			require.Empty(t, res2.FixedFiles)
			require.Equal(t, reportProjection(res.Remaining), reportProjection(res2.Remaining),
				"the remaining report is a fixpoint too")

			for _, u := range targets {
				pf := parseContent(t, u, source[u])
				if hasParseError(pf) {
					continue
				}

				for _, d := range res.Remaining[u] {
					if len(d.Fixes) == 0 {
						continue
					}

					out, applied, _, err := sema.Apply(source[u], d.Fixes)
					require.NoError(t, err)
					require.Empty(t, applied,
						"a converged run leaves no applicable fix: %s %q at %d",
						u.FsPath(), d.Code, d.Span.Start.Offset)
					require.Equal(t, source[u], out)
				}
			}
		}
	})
}

// FuzzFixAllDeterministic: the same workspace and fault-free persist
// produce the same result twice: pass count, applied fixes, fixed files,
// remaining report, and content. Nothing in the loop may depend on map
// iteration order.
func FuzzFixAllDeterministic(f *testing.F) {
	proptest.Fuzz(f, applySeeds, func(t *proptest.T) {
		c := drawFixCase(t)
		pipeline := analyzers.DefaultPipeline(sema.Config{})
		targets := targetURIs(c)

		runOnce := func() (sema.FixResult, map[uri.URI][]byte) {
			view, source := newFixWorkspace(t, c)
			rec := &persistRecorder{source: source, visible: true}

			res, err := pipeline.FixAll(t.Context(), view, targets, rec.persist)
			require.NoError(t, err)

			return res, source
		}

		first, firstSource := runOnce()
		second, secondSource := runOnce()

		require.Equal(t, first.Passes, second.Passes)
		require.Equal(t, first.Applied, second.Applied)
		require.Equal(t, normalizeURIs(first.FixedFiles), normalizeURIs(second.FixedFiles))
		require.Equal(t, reportProjection(first.Remaining), reportProjection(second.Remaining))
		require.Equal(t, firstSource, secondSource)
	})
}

// FuzzFixAllFirstPassMatchesApply is the differential between the batch
// fixer and the pieces it is built from: FixAll's first write for each
// file must be exactly Apply's output for that file's fixes in that pass's
// report.
func FuzzFixAllFirstPassMatchesApply(f *testing.F) {
	proptest.Fuzz(f, applySeeds, func(t *proptest.T) {
		c := drawFixCase(t)
		pipeline := analyzers.DefaultPipeline(sema.Config{})
		targets := targetURIs(c)

		// Independent first pass: one run, then Apply per target.
		refView, _ := newFixWorkspace(t, c)

		report, err := pipeline.Run(t.Context(), refView, targets)
		require.NoError(t, err)

		wantFirst := map[string]string{}

		for _, u := range targets {
			pf, err := refView.Parse(t.Context(), u)
			require.NoError(t, err)

			if hasParseError(pf) {
				continue
			}

			content, err := pf.Content()
			require.NoError(t, err)

			fixes := pipeline.FixesForFile(t.Context(), refView, u, report)
			if len(fixes) == 0 {
				continue
			}

			out, applied, _, err := sema.Apply(content, fixes)
			require.NoError(t, err)

			if len(applied) == 0 {
				continue
			}

			wantFirst[u.FsPath()] = string(out)
		}

		require.NotEmpty(t, wantFirst, "the generated case must be fixable: %v", c.files)

		view, source := newFixWorkspace(t, c)
		rec := &persistRecorder{source: source, visible: true}

		_, err = pipeline.FixAll(t.Context(), view, targets, rec.persist)
		require.NoError(t, err)

		// The first write per file is the first pass's; later passes may
		// write again.
		gotFirst := map[string]string{}

		for _, call := range rec.calls {
			if _, ok := gotFirst[call.file.FsPath()]; !ok {
				gotFirst[call.file.FsPath()] = string(call.content)
			}
		}

		require.Equal(t, wantFirst, gotFirst, "FixAll's first pass must equal run-plus-Apply")
	})
}

// FuzzFixAllPersistFailurePartialSummary injects a persist failure at the
// k-th call: FixAll must return the error naming the file, keep every
// persist that landed before it, count only those fixes, and leave the
// failing file and everything after it untouched.
func FuzzFixAllPersistFailurePartialSummary(f *testing.F) {
	proptest.Fuzz(f, applySeeds, func(t *proptest.T) {
		c := drawFixCase(t)
		pipeline := analyzers.DefaultPipeline(sema.Config{})
		targets := targetURIs(c)

		// Reference run: the persist sequence, and the totals to compare.
		refView, refSource := newFixWorkspace(t, c)

		refRec := &persistRecorder{source: refSource, visible: true}

		ref, err := pipeline.FixAll(t.Context(), refView, targets, refRec.persist)
		require.NoError(t, err)
		require.NotEmpty(t, refRec.calls, "the generated case must be fixable: %v", c.files)

		// k in [1, n]: fail that call. k == n+1: no fault at all.
		k := proptest.Int(t, 1, len(refRec.calls)+1)

		view, source := newFixWorkspace(t, c)
		before := cloneContents(source)

		rec := &persistRecorder{source: source, visible: true, failAt: k}

		got, err := pipeline.FixAll(t.Context(), view, targets, rec.persist)

		if k > len(refRec.calls) {
			require.NoError(t, err, "no fault when k exceeds the persist count")
			require.Equal(t, ref.Applied, got.Applied)
			require.Equal(t, ref.FixedFiles, got.FixedFiles)
			require.Equal(t, refSource, source)

			return
		}

		require.Error(t, err, "the injected failure surfaces")
		require.ErrorContains(t, err, rec.calls[k-1].file.FsPath(), "the error names the file whose persist failed")

		// Everything before the failing call landed; nothing else did.
		prefix := refRec.calls[:k-1]
		want := cloneContents(before)

		for _, call := range prefix {
			want[call.file] = call.content
		}

		require.Equal(t, want, source, "only the persists before the failure land")

		var wantFiles []uri.URI
		for _, call := range prefix {
			wantFiles = append(wantFiles, call.file)
		}

		slices.Sort(wantFiles)
		wantFiles = slices.Compact(wantFiles)
		require.Equal(t, normalizeURIs(wantFiles), normalizeURIs(got.FixedFiles),
			"the partial summary lists exactly the files that landed")

		// Accounting: a prefix of the work, counted after persistence only.
		require.GreaterOrEqual(t, got.Applied, len(wantFiles), "each fixed file carried at least one fix")
		require.Less(t, got.Applied, ref.Applied, "a failed run reports strictly less progress than a complete one")
		require.GreaterOrEqual(t, got.Passes, 1)
		require.LessOrEqual(t, got.Passes, fixPassCap)

		if k > 1 {
			require.NotEmpty(t, got.FixedFiles, "a failure after the first persist keeps the partial summary")
		}
	})
}

// TestFixAllPersistFailureAccounting pins the exact counts around a persist
// failure on a fixed workspace: a failure on the first call leaves nothing
// applied, and on the second call counts only the first file's fix.
func TestFixAllPersistFailureAccounting(t *testing.T) {
	files := func() map[string]string {
		return map[string]string{
			"a.thrift":   "include \"dep.thrift\"\n\nstruct A {\n  1: i32 x,\n}\n",
			"b.thrift":   "include \"dep.thrift\"\n\nstruct B {\n  1: i32 y,\n}\n",
			"dep.thrift": "struct User {\n  1: i32 id,\n}\n",
		}
	}

	newWorkspace := func(t *testing.T, src map[string]string) (*store.View, map[uri.URI][]byte) {
		t.Helper()

		contents := map[uri.URI][]byte{}
		for name, content := range src {
			contents[analyzertest.URI(name)] = []byte(content)
		}

		view := store.NewView(uri.File("/tmp/"), store.NewMemFS(contents), nil)

		for name := range src {
			_, err := view.Parse(t.Context(), analyzertest.URI(name))
			require.NoError(t, err)
		}

		return view, contents
	}

	targets := []uri.URI{analyzertest.URI("a.thrift"), analyzertest.URI("b.thrift")}

	t.Run("first persist fails: nothing is applied", func(t *testing.T) {
		src := files()
		view, contents := newWorkspace(t, src)
		before := cloneContents(contents)

		rec := &persistRecorder{source: contents, visible: true, failAt: 1}

		res, err := analyzers.DefaultPipeline(sema.Config{}).FixAll(t.Context(), view, targets, rec.persist)
		require.Error(t, err)
		require.ErrorContains(t, err, analyzertest.URI("a.thrift").FsPath())

		require.Zero(t, res.Applied, "fixes count only after their content is durable")
		require.Empty(t, res.FixedFiles)
		require.Equal(t, before, contents, "no content landed")
	})

	t.Run("second persist fails: only the first file's fix counts", func(t *testing.T) {
		src := files()
		view, contents := newWorkspace(t, src)

		rec := &persistRecorder{source: contents, visible: true, failAt: 2}

		res, err := analyzers.DefaultPipeline(sema.Config{}).FixAll(t.Context(), view, targets, rec.persist)
		require.Error(t, err)
		require.ErrorContains(t, err, analyzertest.URI("b.thrift").FsPath())

		require.Equal(t, 1, res.Applied)
		require.Equal(t, []uri.URI{analyzertest.URI("a.thrift")}, res.FixedFiles)
		require.Equal(t, "\nstruct A {\n  1: i32 x,\n}\n", string(contents[analyzertest.URI("a.thrift")]),
			"the include line fix landed in a.thrift")
		require.Equal(t, src["b.thrift"], string(contents[analyzertest.URI("b.thrift")]),
			"the failing file is untouched")
	})
}

// FuzzFixAllNilPersistStalls checks the nil-persist path: with no persist
// callback nothing can make the new content visible to the view, so the
// loop behaves like a persist that never reaches the file source:
// it runs to the pass cap and stops, writing nothing.
func FuzzFixAllNilPersistStalls(f *testing.F) {
	proptest.Fuzz(f, applySeeds, func(t *proptest.T) {
		c := drawFixCase(t)
		pipeline := analyzers.DefaultPipeline(sema.Config{})
		targets := targetURIs(c)

		view, source := newFixWorkspace(t, c)
		before := cloneContents(source)

		res, err := pipeline.FixAll(t.Context(), view, targets, nil)
		require.NoError(t, err)
		require.Equal(t, fixPassCap, res.Passes, "no persist, no progress: the cap bounds the loop")
		require.Greater(t, res.Applied, 0)
		require.Equal(t, before, source, "a nil persist writes nothing")

		// Same shape as the invisible-persist fault class.
		view2, source2 := newFixWorkspace(t, c)
		rec := &persistRecorder{source: source2, visible: false}

		res2, err := pipeline.FixAll(t.Context(), view2, targets, rec.persist)
		require.NoError(t, err)
		require.Equal(t, res.Passes, res2.Passes)
		require.Equal(t, res.Applied, res2.Applied)
		require.Equal(t, res.FixedFiles, res2.FixedFiles)
	})
}

// FuzzFixAllInvisiblePersistStopsAtCap checks a persist whose writes never
// reach the view: the loop reaches the pass cap without error and counts
// every applied fix.
func FuzzFixAllInvisiblePersistStopsAtCap(f *testing.F) {
	proptest.Fuzz(f, applySeeds, func(t *proptest.T) {
		c := drawFixCase(t)
		pipeline := analyzers.DefaultPipeline(sema.Config{})
		targets := targetURIs(c)

		view, source := newFixWorkspace(t, c)
		before := cloneContents(source)

		rec := &persistRecorder{source: source, visible: false}

		res, err := pipeline.FixAll(t.Context(), view, targets, rec.persist)
		require.NoError(t, err, "a stalled fixpoint is not an error")
		require.Equal(t, fixPassCap, res.Passes, "the loop reaches the pass cap and stops")
		require.Greater(t, res.Applied, 0)

		// The view never saw the content: its source is untouched.
		require.Equal(t, before, source)

		// Every pass persisted the same content for the same files.
		perFile := map[uri.URI][][]byte{}

		for _, call := range rec.calls {
			perFile[call.file] = append(perFile[call.file], call.content)
		}

		require.NotEmpty(t, perFile)

		for u, contents := range perFile {
			require.Len(t, contents, fixPassCap, "%s is persisted once per pass", u.FsPath())

			for _, content := range contents {
				require.Equal(t, contents[0], content, "%s's content never changes between passes", u.FsPath())
			}
		}

		require.Len(t, rec.calls, fixPassCap*len(perFile))
		require.GreaterOrEqual(t, res.Applied, fixPassCap*len(perFile), "every pass counts its fixes")
	})
}

// toggleAnalyzer reports one diagnostic per file per pass. Paired with
// toggleFixer it makes a fixer whose output is its own next input.
type toggleAnalyzer struct{}

func (toggleAnalyzer) Name() string { return "ToggleAnalyzer" }

func (toggleAnalyzer) Analyze(ctx context.Context, run *sema.Run) error {
	for _, file := range run.Files() {
		pf, err := run.View().Parse(ctx, file)
		if err != nil || pf.AST() == nil {
			continue
		}

		content, err := pf.Content()
		if err != nil || len(content) == 0 {
			continue
		}

		run.Add(file, sema.Diagnostic{
			Code:     "toggle",
			Severity: sema.SeverityWarning,
			Message:  "toggle",
			Span:     offsetSpan(0, len(content)),
		})
	}

	return nil
}

// toggleFixer rewrites the field name of the fixed template back and forth
// ("i32 a," becomes "i32 b," and back), so applying the fix produces the
// input of the next pass.
type toggleFixer struct{}

func (toggleFixer) Fix(_ context.Context, f sema.File, d sema.Diagnostic) []sema.Fix {
	if d.Code != "toggle" {
		return nil
	}

	content, err := f.PF.Content()
	if err != nil {
		return nil
	}

	if i := strings.Index(string(content), "i32 a,"); i >= 0 {
		return []sema.Fix{{Title: "toggle a to b", Edits: []sema.Edit{{Span: offsetSpan(i+4, i+5), NewText: "b"}}}}
	}

	if i := strings.Index(string(content), "i32 b,"); i >= 0 {
		return []sema.Fix{{Title: "toggle b to a", Edits: []sema.Edit{{Span: offsetSpan(i+4, i+5), NewText: "a"}}}}
	}

	return nil
}

// FuzzFixAllOscillatingFixerStopsAtCap: a fixer that undoes its own work
// must hit the pass cap and stop rather than loop forever, and the result
// must account for every applied fix.
func FuzzFixAllOscillatingFixerStopsAtCap(f *testing.F) {
	proptest.Fuzz(f, applySeeds, func(t *proptest.T) {
		var doc strings.Builder

		doc.WriteString("struct S {\n  1: i32 a,\n}\n")

		for i := 0; i < proptest.Int(t, 0, 2); i++ {
			fmt.Fprintf(&doc, "\nstruct N%d {\n  1: i32 n,\n}\n", i)
		}

		content := doc.String()
		main := analyzertest.URI("main.thrift")

		contents := map[uri.URI][]byte{main: []byte(content)}
		view := store.NewView(uri.File("/tmp/"), store.NewMemFS(contents), nil)

		_, err := view.Parse(t.Context(), main)
		require.NoError(t, err)

		pipeline := sema.New(sema.Config{}, []sema.Analyzer{toggleAnalyzer{}}).WithFixers(toggleFixer{})

		rec := &persistRecorder{source: contents, visible: true}

		res, err := pipeline.FixAll(t.Context(), view, []uri.URI{main}, rec.persist)
		require.NoError(t, err)

		require.Equal(t, fixPassCap, res.Passes, "an oscillating fixer exhausts the pass cap")
		require.Equal(t, fixPassCap, res.Applied, "every pass's fix is counted")
		require.Len(t, rec.calls, fixPassCap)
		require.Equal(t, []uri.URI{main}, res.FixedFiles)

		// The flip is a toggle: after an even number of passes the content
		// is back where it started.
		require.Equal(t, content, string(contents[main]))

		// The loop stopped on the cap, not on convergence: the diagnostic is
		// still reported and another pass would still apply a fix.
		report, err := pipeline.Run(t.Context(), view, []uri.URI{main})
		require.NoError(t, err)
		require.NotEmpty(t, report[main])
		require.NotEmpty(t, pipeline.FixesForFile(t.Context(), view, main, report))
	})
}

// drawBrokenCase generates a workspace with a fixable file and one or two
// files that do not parse but carry fixable inline fixes.
func drawBrokenCase(t *proptest.T) fixCase {
	files := map[string]string{
		"dep.thrift":  "struct User {\n  1: i32 id,\n}\n",
		"main.thrift": "include \"dep.thrift\"\n\nstruct S {\n  1: i32 a,\n}\n",
	}

	// A hard parse error in the body, with an unused include above it: the
	// file carries a fixable inline fix the guard must refuse.
	files["broken.thrift"] = "include \"dep.thrift\"\n\nstruct Broken {\n  1: i32 a\n  @@@\n}\n"

	if proptest.Bool(t) {
		files["broken2.thrift"] = "include \"dep.thrift\"\n\nstruct Broken2 {\n  @@@\n}\n"
	}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}

	slices.Sort(names)

	return fixCase{files: files, names: names, targets: names}
}

// FuzzFixAllParseErrorFileUntouched pins the batch fixer's parse-error
// guarantee: a file that does not parse is never edited, its fixable
// diagnostics are reported in Skipped with the parse-error reason, and the
// skip list is the final pass's only.
func FuzzFixAllParseErrorFileUntouched(f *testing.F) {
	proptest.Fuzz(f, applySeeds, func(t *proptest.T) {
		c := drawBrokenCase(t)
		pipeline := analyzers.DefaultPipeline(sema.Config{})
		targets := targetURIs(c)

		view, source := newFixWorkspace(t, c)
		before := cloneContents(source)

		rec := &persistRecorder{source: source, visible: true}

		res, err := pipeline.FixAll(t.Context(), view, targets, rec.persist)
		require.NoError(t, err)

		main := analyzertest.URI("main.thrift")
		require.Equal(t, "\nstruct S {\n  1: i32 a,\n}\n", string(source[main]),
			"the parseable file's unused include line is removed")
		require.Equal(t, 2, res.Passes, "pass 1 fixes main.thrift, pass 2 finds nothing to apply")

		for _, name := range []string{"broken.thrift", "broken2.thrift"} {
			if _, ok := c.files[name]; !ok {
				continue
			}

			u := analyzertest.URI(name)

			require.Equal(t, before[u], source[u], "%s must not be edited", name)
			require.NotContains(t, res.FixedFiles, u)

			for _, call := range rec.calls {
				require.NotEqual(t, u, call.file, "%s must never be persisted", name)
			}

			// Its fixable diagnostics are skipped with the parse-error
			// reason, in report order, exactly once.
			var wantTitles []string

			for _, d := range res.Remaining[u] {
				for _, fx := range d.Fixes {
					wantTitles = append(wantTitles, fx.Title)
				}
			}

			var gotTitles []string

			for _, s := range res.Skipped {
				if s.File != u {
					continue
				}

				require.Equal(t, "file has parse errors", s.Reason)
				gotTitles = append(gotTitles, s.Fix.Title)
			}

			require.NotEmpty(t, wantTitles, "%s carries fixable diagnostics", name)
			require.Equal(t, wantTitles, gotTitles, "%s's skips are its inline fixes, once")
		}

		for _, s := range res.Skipped {
			require.NotEqual(t, main, s.File, "main.thrift has nothing skipped")
		}
	})
}
