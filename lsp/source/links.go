// Package links computes document links: include paths resolving to their
// target files. Pure over the snapshot: parsing and file I/O happen in the
// caller.
package source

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
