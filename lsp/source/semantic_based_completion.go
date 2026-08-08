package source

import (
	"context"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/cache"
)

type Interface interface {
	// Completion returns the completion items for the request, the edit
	// range, and whether the list was truncated by the item cap (the LSP
	// isIncomplete flag).
	Completion(ctx context.Context, ss *cache.Snapshot, cmp *CompletionRequest) ([]*CompletionItem, protocol.Range, bool, error)
}

func BuildCompletionItem(candidate Candidate) *CompletionItem {
	return &CompletionItem{
		Label:            candidate.showText,
		Detail:           candidate.showText,
		InsertText:       candidate.insertText,
		InsertTextFormat: candidate.format,
		Kind:             protocol.CompletionItemKindText,
		Deprecated:       false,
		Documentation:    "",
	}
}
