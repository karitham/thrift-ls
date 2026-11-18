package mapper

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
)

func TestApplyEditsSameStartTies(t *testing.T) {
	replacement := protocol.TextEdit{
		Range:   protocol.Range{Start: pos(2), End: pos(5)},
		NewText: "R",
	}
	insertion := protocol.TextEdit{
		Range:   protocol.Range{Start: pos(2), End: pos(2)},
		NewText: "I",
	}
	first := protocol.TextEdit{Range: insertion.Range, NewText: "1"}
	second := protocol.TextEdit{Range: insertion.Range, NewText: "2"}

	for _, tt := range []struct {
		name  string
		edits []protocol.TextEdit
		want  string
	}{
		{"replacement then insertion", []protocol.TextEdit{replacement, insertion}, "abIRf"},
		{"insertion then replacement", []protocol.TextEdit{insertion, replacement}, "abIRf"},
		{"two insertions", []protocol.TextEdit{first, second}, "ab12cdef"},
		{"two insertions reversed", []protocol.TextEdit{second, first}, "ab21cdef"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewMapper([]byte("abcdef")).ApplyEdits(tt.edits)
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(got))
		})
	}
}
