package mapper

import (
	"cmp"
	"slices"
	"strings"
	"testing"

	"github.com/karitham/thrift-ls/internal/proptest"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
)

// editSpec is one edit in byte offsets: [start, end) replaced by text. The
// property works in offsets and converts through the mapper, so it covers
// multibyte content without hand-computing UTF-16 columns.
type editSpec struct {
	start, end int
	text       string
}

// modelApply is a second implementation of ApplyEdits' tie rule.
func modelApply(content string, specs []editSpec) string {
	sorted := slices.Clone(specs)

	slices.SortStableFunc(sorted, func(a, b editSpec) int {
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

	out := content
	for i := len(sorted) - 1; i >= 0; i-- {
		edit := sorted[i]
		out = out[:edit.start] + edit.text + out[edit.end:]
	}

	return out
}

func drawSpecs(t *proptest.T, boundaries []int) []editSpec {
	anchor := proptest.Pick(t, boundaries)

	// The anchor is either an insertion or a replacement starting at anchor.
	anchorEnd := anchor
	if proptest.Bool(t) {
		var ends []int
		for _, boundary := range boundaries {
			if boundary > anchor {
				ends = append(ends, boundary)
			}
		}

		if len(ends) > 0 {
			anchorEnd = proptest.Pick(t, ends)
		}
	}

	specs := []editSpec{{start: anchor, end: anchorEnd, text: proptest.Pick(t, []string{"R", "", "replacement"})}}

	// Insertions at the same offset as the anchor: the tie class under test.
	insertions := proptest.Int(t, 0, 3)
	for i := 0; i < insertions; i++ {
		specs = append(specs, editSpec{start: anchor, end: anchor, text: proptest.Pick(t, []string{"1", "2", "", "×"})})
	}

	// Optionally one insertion elsewhere, outside the anchor's range: it
	// must not disturb the tie group.
	if proptest.Bool(t) {
		var outside []int
		for _, boundary := range boundaries {
			if boundary < anchor || boundary > anchorEnd {
				outside = append(outside, boundary)
			}
		}

		if len(outside) > 0 {
			outsideAt := proptest.Pick(t, outside)
			specs = append(specs, editSpec{
				start: outsideAt,
				end:   outsideAt,
				text:  proptest.Pick(t, []string{"x", "yy"}),
			})
		}
	}

	return specs
}

func toTextEdits(tb require.TestingT, content []byte, specs []editSpec) []protocol.TextEdit {
	mapper := NewMapper(content)

	edits := make([]protocol.TextEdit, 0, len(specs))
	for _, spec := range specs {
		start, err := mapper.OffsetToLSPPosition(spec.start)
		require.NoError(tb, err)

		end, err := mapper.OffsetToLSPPosition(spec.end)
		require.NoError(tb, err)

		edits = append(edits, protocol.TextEdit{
			Range:   protocol.Range{Start: start, End: end},
			NewText: spec.text,
		})
	}

	return edits
}

func FuzzApplyEditsSameStartTie(f *testing.F) {
	proptest.Fuzz(f, proptest.Corpus{
		[]byte("abc"),
		[]byte("aé😀 b\n"),
		[]byte("insertion and replacement"),
	}, func(t *proptest.T) {
		alphabet := []string{"a", "b", "c", "é", "😀", "日", "\n", " "}

		content := strings.Join(proptest.Slice(t, proptest.SampledFrom(alphabet), 0, 12), "")
		boundaries := proptest.RuneBoundaries(content)
		specs := drawSpecs(t, boundaries)

		orders := [][]editSpec{
			specs,
			proptest.Permute(t, specs),
		}

		var bySecondOrder string

		for i, order := range orders {
			// Insertions at one offset keep their argument order, so the
			// expectation is per order, not per edit set.
			want := modelApply(content, order)
			edits := toTextEdits(t, []byte(content), order)

			got, err := NewMapper([]byte(content)).ApplyEdits(edits)
			require.NoError(t, err, "edits must be accepted")
			require.Equal(t, want, string(got), "same-start splice, argument order %v", order)

			if i == 1 {
				bySecondOrder = want
			}

			deleted, inserted := 0, 0
			for _, spec := range order {
				deleted += spec.end - spec.start
				inserted += len(spec.text)
			}

			require.Equal(t, len(content)-deleted+inserted, len(got), "length arithmetic")
		}

		// With at most one insertion per offset every edit is distinguishable
		// by its range, so argument order must stop mattering.
		if distinguishable(specs) {
			require.Equal(t, modelApply(content, specs), bySecondOrder, "argument order must not matter when offsets differ")
		}
	})
}

// distinguishable reports whether every edit has a distinct range, so no
// argument order can change the outcome.
func distinguishable(specs []editSpec) bool {
	seen := make(map[[2]int]struct{}, len(specs))

	for _, spec := range specs {
		key := [2]int{spec.start, spec.end}
		if _, ok := seen[key]; ok {
			return false
		}

		seen[key] = struct{}{}
	}

	return true
}
