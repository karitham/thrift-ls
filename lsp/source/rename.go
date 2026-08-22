package source

import (
	"context"
	"fmt"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

// PrepareRename returns the range of the identifier under the cursor when
// renaming is supported: definition names, const values, and services.
func PrepareRename(ctx context.Context, view *cache.View, file uri.URI, pos protocol.Position) (res *protocol.Range, err error) {
	pf, target, err := resolveTarget(ctx, view, file, pos)
	if err != nil {
		return res, err
	}

	switch target.kind {
	case TargetDefinition, TargetConstValue, TargetService:
		rg := nodeRange(pf, target.node)

		return &rg, nil
	case TargetTypeName:
		// Rename supports type references; basic types are the only
		// position it rejects, so prepare must reject them too.
		ft := target.parent.(*syntax.FieldType)
		if typeReferenceName(ft) == "" || IsBasicType(typeReferenceName(ft)) {
			return nil, fmt.Errorf("rename not supported for basic types")
		}

		rg := nodeRange(pf, target.node)

		return &rg, nil
	}

	return nil, fmt.Errorf("rename not supported at this position")
}

// Rename renames the definition under the cursor and all its references,
// preserving include qualifiers on qualified references (user.Test becomes
// user.newtext, not newtext).
func Rename(ctx context.Context, view *cache.View, file uri.URI, pos protocol.Position, newName string) (res *protocol.WorkspaceEdit, err error) {
	pf, target, err := resolveTarget(ctx, view, file, pos)
	if err != nil {
		return res, err
	}

	var refs []indexHit

	switch target.kind {
	case TargetTypeName:
		ft := target.parent.(*syntax.FieldType)
		if typeReferenceName(ft) == "" || IsBasicType(typeReferenceName(ft)) {
			return nil, fmt.Errorf("rename not supported for basic types")
		}

		refs, err = searchTypeNameRefs(ctx, NewIndex(view), view, pf, target)
		if err != nil {
			return nil, err
		}

	case TargetConstValue:
		refs, err = searchConstValueRefs(ctx, NewIndex(view), view, pf, target)
		if err != nil {
			return nil, err
		}

	case TargetService:
		svcName := target.identifier().Text
		if !strings.Contains(svcName, ".") {
			svcName = fmt.Sprintf("%s.%s", includeNameOf(file), svcName)
		} else {
			include, _ := parseIdent(file, pf.AST().Includes(), svcName)

			resolver := view.Resolver()
			if path := resolver.GetIncludePath(pf.AST(), include); path != "" {
				file = resolver.ResolveInclude(file, path)
			}
		}

		refs, err = searchServiceRefs(ctx, NewIndex(view), view, file, svcName)
		if err != nil {
			return nil, err
		}

	case TargetDefinition:
		refs, err = searchDefRefs(ctx, NewIndex(view), view, file, pf, target)
		if err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("rename not supported at this position")
	}

	// The definition under the cursor itself.
	refs = append(refs, indexHit{
		loc: protocol.Location{
			URI:   file,
			Range: nodeRange(pf, target.node),
		},
	})

	return convertHitsToWorkspaceEdit(refs, newName), nil
}

// convertHitsToWorkspaceEdit groups the edits by file. A reference whose
// text has an include qualifier (user.Test) keeps the qualifier: the new
// text becomes user.newtext.
func convertHitsToWorkspaceEdit(refs []indexHit, newName string) *protocol.WorkspaceEdit {
	changes := make(map[uri.URI][]protocol.TextEdit)

	for i := range refs {
		text := newName
		if dot := strings.LastIndexByte(refs[i].text, '.'); dot >= 0 {
			text = refs[i].text[:dot+1] + newName
		}

		changes[refs[i].loc.URI] = append(changes[refs[i].loc.URI], protocol.TextEdit{
			Range:   refs[i].loc.Range,
			NewText: text,
		})
	}

	return &protocol.WorkspaceEdit{
		Changes: changes,
	}
}
