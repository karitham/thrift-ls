package lsp

import (
	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/store"
)

// FileChangeFromLSPDidChange converts a didChange payload into file
// changes. The server advertises whole-document sync, so whole-document
// events map one-to-one; incremental (partial) events are a client
// protocol violation and are skipped.
func FileChangeFromLSPDidChange(params *protocol.DidChangeTextDocumentParams) []*store.FileChange {
	changes := make([]*store.FileChange, 0, len(params.ContentChanges))
	for i := range params.ContentChanges {
		event, ok := params.ContentChanges[i].(*protocol.TextDocumentContentChangeWholeDocument)
		if !ok {
			continue
		}

		changes = append(changes, &store.FileChange{
			URI:     params.TextDocument.URI,
			Version: int(params.TextDocument.Version),
			Content: []byte(event.Text),
			From:    store.FileChangeTypeDidChange,
		})
	}

	return changes
}
