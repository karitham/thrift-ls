package lsp

import (
	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/vfs"
)

// FileChangeFromLSPDidChange converts a didChange payload into file
// changes. The server advertises whole-document sync, so whole-document
// events map one-to-one; incremental (partial) events are a client
// protocol violation and are skipped.
func FileChangeFromLSPDidChange(params *protocol.DidChangeTextDocumentParams) []*vfs.FileChange {
	changes := make([]*vfs.FileChange, 0, len(params.ContentChanges))
	for i := range params.ContentChanges {
		event, ok := params.ContentChanges[i].(*protocol.TextDocumentContentChangeWholeDocument)
		if !ok {
			continue
		}

		changes = append(changes, &vfs.FileChange{
			URI:     params.TextDocument.URI,
			Version: int(params.TextDocument.Version),
			Content: []byte(event.Text),
			From:    vfs.FileChangeTypeDidChange,
		})
	}

	return changes
}
