package symbols

import (
	"context"
	"sort"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
)

// WorkspaceSymbols returns the workspace symbols matching query: every
// top-level definition and its members across all workspace folders,
// folders and files ordered by URI, symbols in source order. An empty
// query matches everything; the result is capped at maxResults (0 means
// unlimited). Matching is case-insensitive substring on the symbol name.
func WorkspaceSymbols(ctx context.Context, session *cache.Session, query string, maxResults int) []protocol.SymbolInformation {
	res := make([]protocol.SymbolInformation, 0, 64)
	q := strings.ToLower(query)

	views := session.Views()
	sort.Slice(views, func(i, j int) bool { return views[i].Folder() < views[j].Folder() })

	for _, view := range views {
		snapshot, release := view.Snapshot()

		for _, file := range view.KnownFiles() {
			for _, sym := range documentSymbolsFlat(ctx, snapshot, file) {
				if q != "" && !strings.Contains(strings.ToLower(sym.Name), q) {
					continue
				}

				res = append(res, sym)
				if maxResults > 0 && len(res) >= maxResults {
					release()

					return res
				}
			}
		}

		release()
	}

	return res
}

// documentSymbolsFlat returns the document symbols of a file flattened
// into workspace symbols: each child carries its parent's name as the
// container, and the location points at the symbol's name.
func documentSymbolsFlat(ctx context.Context, ss *cache.Snapshot, file uri.URI) []protocol.SymbolInformation {
	syms := make([]protocol.SymbolInformation, 0, 16)

	for _, sym := range DocumentSymbols(ctx, ss, file) {
		flattenSymbol(sym, file, "", &syms)
	}

	return syms
}

func flattenSymbol(sym *protocol.DocumentSymbol, file uri.URI, container string, out *[]protocol.SymbolInformation) {
	info := protocol.SymbolInformation{
		BaseSymbolInformation: protocol.BaseSymbolInformation{
			Name: sym.Name,
			Kind: sym.Kind,
		},
		Location: protocol.Location{URI: file, Range: sym.SelectionRange},
	}
	if container != "" {
		info.ContainerName = new(container)
	}

	*out = append(*out, info)

	for i := range sym.Children {
		flattenSymbol(&sym.Children[i], file, sym.Name, out)
	}
}
