package mapper

import (
	"cmp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"pgregory.net/rapid"
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

// runeBoundaries returns every byte offset that starts a rune, plus the end
// of the content: positions are only meaningful at rune boundaries.
func runeBoundaries(content string) []int {
	offsets := []int{0}

	for offset := range content {
		if offset > 0 {
			offsets = append(offsets, offset)
		}
	}

	if len(content) > 0 {
		offsets = append(offsets, len(content))
	}

	return offsets
}

func drawSpecs(t *rapid.T, boundaries []int) []editSpec {
	anchor := rapid.SampledFrom(boundaries).Draw(t, "anchor")

	// The anchor is either an insertion or a replacement starting at anchor.
	anchorEnd := anchor
	if rapid.Bool().Draw(t, "replace") {
		var ends []int
		for _, boundary := range boundaries {
			if boundary > anchor {
				ends = append(ends, boundary)
			}
		}

		if len(ends) > 0 {
			anchorEnd = rapid.SampledFrom(ends).Draw(t, "anchorEnd")
		}
	}

	specs := []editSpec{{start: anchor, end: anchorEnd, text: rapid.SampledFrom([]string{"R", "", "replacement"}).Draw(t, "replacementText")}}

	// Insertions at the same offset as the anchor: the tie class under test.
	insertions := rapid.IntRange(0, 3).Draw(t, "sameStartInsertions")
	for i := 0; i < insertions; i++ {
		specs = append(specs, editSpec{start: anchor, end: anchor, text: rapid.SampledFrom([]string{"1", "2", "", "×"}).Draw(t, "insertionText")})
	}

	// Optionally one insertion elsewhere, outside the anchor's range: it
	// must not disturb the tie group.
	if rapid.Bool().Draw(t, "disjoint") {
		var outside []int
		for _, boundary := range boundaries {
			if boundary < anchor || boundary > anchorEnd {
				outside = append(outside, boundary)
			}
		}

		if len(outside) > 0 {
			outsideAt := rapid.SampledFrom(outside).Draw(t, "outside")
			specs = append(specs, editSpec{
				start: outsideAt,
				end:   outsideAt,
				text:  rapid.SampledFrom([]string{"x", "yy"}).Draw(t, "outsideText"),
			})
		}
	}

	return specs
}

func toTextEdits(t *rapid.T, content []byte, specs []editSpec) []protocol.TextEdit {
	mapper := NewMapper(content)

	edits := make([]protocol.TextEdit, 0, len(specs))
	for _, spec := range specs {
		start, err := mapper.OffsetToLSPPosition(spec.start)
		require.NoError(t, err)

		end, err := mapper.OffsetToLSPPosition(spec.end)
		require.NoError(t, err)

		edits = append(edits, protocol.TextEdit{
			Range:   protocol.Range{Start: start, End: end},
			NewText: spec.text,
		})
	}

	return edits
}

// TestApplyEditsSameStartTieProperty checks ApplyEdits' tie rule over generated
// content and edit orders.
func TestApplyEditsSameStartTieProperty(t *testing.T) {
	alphabet := []string{"a", "b", "c", "é", "😀", "日", "\n", " "}

	rapid.Check(t, func(t *rapid.T) {
		content := strings.Join(rapid.SliceOfN(rapid.SampledFrom(alphabet), 0, 12).Draw(t, "content"), "")
		boundaries := runeBoundaries(content)
		specs := drawSpecs(t, boundaries)

		orders := [][]editSpec{
			slices.Clone(specs),
			rapid.Permutation(specs).Draw(t, "argumentOrder"),
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
