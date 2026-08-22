package source

import (
	"context"
	"path/filepath"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
)

// RenameFileEdits returns, per including file, the edits retargeting every
// include of oldURI at newURI's location. Only direct includers carry an
// include literal naming the file; transitive dependents resolve through
// them and need no edit.
func RenameFileEdits(ctx context.Context, view *cache.View, oldURI, newURI uri.URI) (map[uri.URI][]protocol.TextEdit, error) {
	res := make(map[uri.URI][]protocol.TextEdit)

	for _, f := range view.Includers(oldURI) {
		pf, err := view.Parse(ctx, f)
		if err != nil {
			return nil, err
		}

		if pf.AST() == nil {
			return nil, errNoAST
		}

		includerDir := filepath.Dir(f.FsPath())
		resolver := view.Resolver()

		var edits []protocol.TextEdit

		for _, inc := range pf.AST().Includes() {
			if inc.Path == nil {
				continue
			}

			if resolver.ResolveInclude(f, inc.PathText()) != oldURI {
				continue
			}

			text, ok := renamedIncludeText(inc.Path.Text, oldURI.FsPath(), newURI.FsPath(), includerDir)
			if !ok {
				continue
			}

			edits = append(edits, protocol.TextEdit{
				Range:   tokenRange(pf, inc.Path),
				NewText: text,
			})
		}

		if len(edits) > 0 {
			res[f] = edits
		}
	}

	return res, nil
}

// renamedIncludeText rewrites a quoted include literal for a file rename,
// preserving the original quote character. When the old resolution lived
// inside the includer's directory tree, the literal becomes the
// includer-relative path to the new location ("renamed.thrift",
// "../shared/renamed.thrift"); when it resolved through an include-path
// directory (-I), only the literal's final segment swaps, so the lookup
// shape stays intact.
func renamedIncludeText(literal, oldPath, newPath, includerDir string) (string, bool) {
	if len(literal) < 2 {
		return "", false
	}

	path := strings.Trim(literal, "\"'")
	quote := literal[0]

	var out string

	inIncluderTree := strings.HasPrefix(oldPath, includerDir+string(filepath.Separator)) ||
		filepath.Dir(oldPath) == includerDir
	if inIncluderTree {
		rel, err := filepath.Rel(includerDir, newPath)
		if err != nil {
			return "", false
		}

		out = filepath.ToSlash(rel)
	} else {
		out = filepath.ToSlash(filepath.Join(filepath.Dir(path), filepath.Base(newPath)))
	}

	if out == path {
		return "", false
	}

	return string(quote) + out + string(quote), true
}
