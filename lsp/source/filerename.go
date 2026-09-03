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

			if resolver.ResolveInclude(ctx, f, inc.PathText()) != oldURI {
				continue
			}

			text, ok := renamedIncludeText(inc.Path.Text, oldURI.FsPath(), newURI.FsPath(), includerDir, resolver.IncludePaths())
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
// preserving the original quote character. The default form is relative to
// the includer's directory (which thrift resolves first, across ".."
// segments included); when the old resolution lived under an include-path
// root (-I) and the new location lives under the same root, the literal is
// written relative to that root so its lookup shape survives moves between
// the root's subdirectories.
func renamedIncludeText(literal, oldPath, newPath, includerDir string, includePaths []string) (string, bool) {
	if len(literal) < 2 {
		return "", false
	}

	path := strings.Trim(literal, "\"'")
	quote := literal[0]

	out := includerRelative(includerDir, newPath)

	for _, root := range includePaths {
		if !underRoot(oldPath, root) || !underRoot(newPath, root) {
			continue
		}

		if rel, err := filepath.Rel(root, newPath); err == nil {
			out = filepath.ToSlash(rel)
		}

		break
	}

	if out == path {
		return "", false
	}

	return string(quote) + out + string(quote), true
}

// includerRelative writes newPath relative to the includer's directory.
func includerRelative(includerDir, newPath string) string {
	rel, err := filepath.Rel(includerDir, newPath)
	if err != nil {
		return filepath.ToSlash(filepath.Base(newPath))
	}

	return filepath.ToSlash(rel)
}

// underRoot reports whether p lies inside root's tree.
func underRoot(p, root string) bool {
	rel, err := filepath.Rel(root, p)

	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
