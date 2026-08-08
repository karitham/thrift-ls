package source

import (
	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/cache"
)

type CompletionRequest struct {
	Pos protocol.Position
	Fh  cache.FileHandle
}

type CompletionItem struct {
	// Label holds the primary text user sees
	Label string

	// Detail a human-readable string with additional information
	// about this item, like type or symbol information.
	Detail string

	// InsertText holds the text to insert when user selects this completion.
	// It may be same with Label
	InsertText       string
	InsertTextFormat protocol.InsertTextFormat

	Kind       protocol.CompletionItemKind
	Deprecated bool

	// Documentation holds document text for this completion
	Documentation string
}
