// Package links computes document links: include paths resolving to their
// target files. Pure over the snapshot: parsing and file I/O happen in the
// caller.
package links

import (
	"context"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

// Links returns the document links of a file, one per include and
// cpp_include, targeting the resolved file.
func Links(ctx context.Context, ss *cache.Snapshot, file uri.URI) []protocol.DocumentLink {
	pf, err := ss.Parse(ctx, file)
	if err != nil || pf.AST() == nil {
		return nil
	}

	doc := pf.AST()
	resolver := ss.Resolver()

	var out []protocol.DocumentLink

	add := func(path *syntax.Token) {
		if path == nil {
			return
		}

		text := strings.Trim(path.Text, "\"'")
		if text == "" {
			return
		}

		target := resolver.ResolveInclude(file, text)
		out = append(out, protocol.DocumentLink{
			Range:  tokenRange(doc, path),
			Target: &target,
		})
	}

	for _, inc := range doc.Includes() {
		add(inc.Path)
	}

	for _, inc := range doc.CPPIncludes() {
		add(inc.Path)
	}

	return out
}

// tokenRange converts a token's span into a protocol range.
func tokenRange(doc *syntax.Document, tok *syntax.Token) protocol.Range {
	start := doc.TokenPosition(doc.TokenIndex(tok))
	end := doc.TokenEndPosition(doc.TokenIndex(tok))

	return protocol.Range{
		Start: protocol.Position{Line: uint32(start.Line - 1), Character: uint32(start.Col - 1)},
		End:   protocol.Position{Line: uint32(end.Line - 1), Character: uint32(end.Col - 1)},
	}
}
