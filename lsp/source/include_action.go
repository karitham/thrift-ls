package source

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

// MakeRemoveUnusedIncludeAction returns the quickfix that deletes the
// include line for an "unused include" diagnostic on the selection. It
// returns nil when no such diagnostic overlaps the selection.
func MakeRemoveUnusedIncludeAction(ctx context.Context, ss *cache.Snapshot, fh cache.FileHandle, rng protocol.Range, diags []protocol.Diagnostic) (*protocol.CodeAction, error) {
	pf, err := ss.Parse(ctx, fh.URI())
	if err != nil {
		return nil, err
	}

	if pf.AST() == nil || len(pf.Errors()) > 0 {
		return nil, nil
	}

	inc := unusedIncludeAt(pf, rng, diags)
	if inc == nil {
		return nil, nil
	}

	// The include statement is a statement of its own: delete from the
	// first token to the end of its line. Tokens carry 1-based lines.
	start, end := pf.AST().Range(inc)
	span := toLSPRange(pf, start, end)

	return &protocol.CodeAction{
		Title: fmt.Sprintf("Remove unused include %q", inc.PathText()),
		Kind:  new(protocol.CodeActionKindQuickFix),
		Edit: &protocol.WorkspaceEdit{
			Changes: map[uri.URI][]protocol.TextEdit{
				fh.URI(): {
					{
						Range: protocol.Range{
							Start: protocol.Position{Line: span.Start.Line, Character: 0},
							End: protocol.Position{
								Line:      span.End.Line + 1,
								Character: 0,
							},
						},
						NewText: "",
					},
				},
			},
		},
	}, nil
}

// unusedIncludeAt returns the include statement an "unused include"
// diagnostic on the selection refers to, or nil.
func unusedIncludeAt(pf *cache.ParsedFile, rng protocol.Range, diags []protocol.Diagnostic) *syntax.Include {
	var target protocol.Range
	found := false

	for _, d := range diags {
		if hasCode(d, CodeUnusedInclude) && RangesOverlap(rng, d.Range) {
			target = d.Range
			found = true

			break
		}
	}

	if !found {
		return nil
	}

	for _, inc := range pf.AST().Includes() {
		if RangesOverlap(target, nodeRange(pf, inc)) {
			return inc
		}
	}

	return nil
}

// MakeAddMissingIncludeAction returns the quickfix that adds an include of
// the file defining a type flagged "field type doesn't exist" on the
// selection. The definition is searched in every thrift file under the
// workspace folder, so the fix works across the whole project. It returns
// nil when the selection has no such diagnostic, the referenced type is
// not found anywhere, or the current file cannot be edited (parse errors).
func MakeAddMissingIncludeAction(ctx context.Context, ss *cache.Snapshot, fh cache.FileHandle, rng protocol.Range, diags []protocol.Diagnostic) (*protocol.CodeAction, error) {
	pf, err := ss.Parse(ctx, fh.URI())
	if err != nil {
		return nil, err
	}

	if pf.AST() == nil || len(pf.Errors()) > 0 {
		return nil, nil
	}

	name := missingTypeAt(ctx, ss, fh, rng, diags)
	if name == "" {
		return nil, nil
	}

	defFile, ok := findTypeInFolder(ctx, ss, fh.URI(), name)
	if !ok || defFile == fh.URI() {
		return nil, nil
	}

	incPath, err := filepath.Rel(path.Dir(fh.URI().Path()), defFile.Path())
	if err != nil {
		return nil, nil
	}
	incPath = filepath.ToSlash(incPath)

	// Insert after the last include statement, or at the top of the file.
	insert := protocol.Position{}
	if includes := pf.AST().Includes(); len(includes) > 0 {
		last := includes[len(includes)-1]
		_, end := pf.AST().Range(last)
		insert = toLSPPosition(pf, end)
		insert.Character = 0
		insert.Line++
	}

	return &protocol.CodeAction{
		Title: fmt.Sprintf("Add include %q", incPath),
		Kind:  new(protocol.CodeActionKindQuickFix),
		Edit: &protocol.WorkspaceEdit{
			Changes: map[uri.URI][]protocol.TextEdit{
				fh.URI(): {
					{
						Range:   protocol.Range{Start: insert, End: insert},
						NewText: fmt.Sprintf("include %q\n", incPath),
					},
				},
			},
		},
	}, nil
}

// missingTypeAt returns the type name of a "field type doesn't exist"
// diagnostic on the selection, or "".
func missingTypeAt(ctx context.Context, ss *cache.Snapshot, fh cache.FileHandle, rng protocol.Range, diags []protocol.Diagnostic) string {
	overlap := false

	for _, d := range diags {
		if hasCode(d, CodeUndefinedType) && RangesOverlap(rng, d.Range) {
			overlap = true

			break
		}
	}

	if !overlap {
		return ""
	}

	_, target, err := resolveTarget(ctx, ss, fh.URI(), rng.Start)
	if err != nil {
		return ""
	}

	if target.kind != TargetTypeName {
		return ""
	}

	ft, ok := target.parent.(*syntax.FieldType)
	if !ok {
		return ""
	}

	return typeReferenceName(ft)
}

// findTypeInFolder searches every thrift file under the workspace folder
// (excluding file) for a definition of name, returning the first match in
// lexical order.
func findTypeInFolder(ctx context.Context, ss *cache.Snapshot, file uri.URI, name string) (uri.URI, bool) {
	root := ss.View().Folder().Path()
	if root == "" {
		return "", false
	}

	var files []string

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		if !d.IsDir() && strings.HasSuffix(d.Name(), ".thrift") && uri.File(p) != file {
			files = append(files, p)
		}

		return nil
	})
	if err != nil {
		return "", false
	}

	sort.Strings(files)

	for _, p := range files {
		pf, err := ss.Parse(ctx, uri.File(p))
		if err != nil || pf.AST() == nil {
			continue
		}

		if _, ok := pf.Definitions()[name]; ok {
			return uri.File(p), true
		}
	}

	return "", false
}
