package lsp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
)

// TestFileChangeFromLSPDidChange pins whole-document sync semantics: the
// server advertises TextDocumentSyncKindFull, so whole-document events map
// one-to-one and incremental (partial) events are skipped.
func TestFileChangeFromLSPDidChange(t *testing.T) {
	tests := []struct {
		name        string
		content     []protocol.TextDocumentContentChangeEvent
		wantContent []byte
		want        int // number of file changes produced
	}{
		{
			name: "whole document",
			content: []protocol.TextDocumentContentChangeEvent{
				&protocol.TextDocumentContentChangeWholeDocument{Text: "struct HTT {}"},
			},
			wantContent: []byte("struct HTT {}"),
			want:        1,
		},
		{
			name: "incremental change is skipped",
			content: []protocol.TextDocumentContentChangeEvent{
				&protocol.TextDocumentContentChangePartial{
					Range: protocol.Range{
						Start: protocol.Position{Line: 0, Character: 7},
						End:   protocol.Position{Line: 0, Character: 10},
					},
					Text: "HoukagoTeaTime",
				},
			},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changes := FileChangeFromLSPDidChange(&protocol.DidChangeTextDocumentParams{
				TextDocument: protocol.VersionedTextDocumentIdentifier{
					TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: "file:///tmp/song.thrift"},
				},
				ContentChanges: tt.content,
			})

			require.Len(t, changes, tt.want)

			if tt.want == 1 {
				assert.Equal(t, tt.wantContent, changes[0].Content)
			}
		})
	}
}
