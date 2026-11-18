package mapper

import (
	"cmp"
	"fmt"
	"slices"

	"go.lsp.dev/protocol"
)

// ApplyEdits returns the mapped content with the edits applied. Edits must
// not overlap; they may arrive in any order. Positions are resolved from
// LSP (UTF-16) coordinates via the mapper, and later edits apply first so
// earlier offsets stay valid. Same-start edits follow sema.Apply's tie order.
func (m *Mapper) ApplyEdits(edits []protocol.TextEdit) ([]byte, error) {
	type pending struct {
		start, end int
		text       string
	}

	all := make([]pending, 0, len(edits))

	for _, e := range edits {
		start, err := m.offsetAt(e.Range.Start)
		if err != nil {
			return nil, err
		}

		end, err := m.offsetAt(e.Range.End)
		if err != nil {
			return nil, err
		}

		all = append(all, pending{start, end, e.NewText})
	}

	// Splicing back to front keeps earlier offsets valid; on equal starts,
	// insertions go first so their text lands before a replacement's.
	slices.SortStableFunc(all, func(a, b pending) int {
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

	out := m.content

	for i := len(all) - 1; i >= 0; i-- {
		e := all[i]

		if e.start < 0 || e.start > e.end || e.end > len(m.content) || e.end > len(out) {
			return nil, fmt.Errorf("invalid edit range [%d:%d], total content: %d", e.start, e.end, len(m.content))
		}

		out = slices.Concat(out[:e.start], []byte(e.text), out[e.end:])
	}

	return out, nil
}

// offsetAt resolves an LSP (UTF-16) position to a byte offset in the mapped
// content.
func (m *Mapper) offsetAt(pos protocol.Position) (int, error) {
	p, err := m.LSPPosToParserPosition(protocol.Position{Line: pos.Line, Character: pos.Character})
	if err != nil {
		return 0, err
	}

	return p.Offset, nil
}
