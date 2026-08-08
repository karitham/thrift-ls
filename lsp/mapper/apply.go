package mapper

import (
	"fmt"
	"sort"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/types"
)

// ApplyEdits returns the mapped content with the edits applied. Edits must
// not overlap; they may arrive in any order. Positions are resolved from
// LSP (UTF-16) coordinates via the mapper, and later edits apply first so
// earlier offsets stay valid.
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

	// Later edits apply first so earlier offsets stay valid.
	sort.Slice(all, func(i, j int) bool { return all[i].start > all[j].start })

	buf := m.content

	for _, e := range all {
		if e.start < 0 || e.end > len(buf) || e.start > e.end {
			return nil, fmt.Errorf("invalid edit range [%d:%d], total content: %d", e.start, e.end, len(buf))
		}

		buf = append(append(buf[:e.start:e.start], e.text...), buf[e.end:]...)
	}

	return buf, nil
}

// offsetAt resolves an LSP (UTF-16) position to a byte offset in the mapped
// content.
func (m *Mapper) offsetAt(pos protocol.Position) (int, error) {
	p, err := m.LSPPosToParserPosition(types.Position{Line: pos.Line, Character: pos.Character})

	return p.Offset, err
}
