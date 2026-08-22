package source

import (
	"context"
	"fmt"
	"sort"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

// MakeFieldQualifierAction returns the "Make field required" and "Make
// field optional" code actions for the fields whose declaration line the
// selection covers. Every covered field yields one action per qualifier
// it does not already carry: an unqualified field gets both, a required
// field gets "Make field optional", and vice versa. Union fields never
// offer "Make field required": unions have no required members.
// pickedFieldAction pairs an action with the declaration offset of its
// field, so the actions can be ordered into document order.
type pickedFieldAction struct {
	offset int
	code   protocol.CodeAction
}

func MakeFieldQualifierAction(ctx context.Context, view *cache.View, fh cache.FileHandle, rng protocol.Range) ([]protocol.CodeAction, error) {
	pf, err := view.Parse(ctx, fh.URI())
	if err != nil {
		return nil, err
	}

	if pf.AST() == nil || len(pf.Errors()) > 0 {
		return nil, nil
	}

	var picked []pickedFieldAction

	pf.AST().WalkFieldLists(func(fields []*syntax.Field, kind syntax.FieldListKind) {
		for _, field := range fields {
			// TokenPosition is 1-based; selection lines are 0-based.
			line := pf.AST().TokenPosition(field.TokStart()).Line - 1
			if line < int(rng.Start.Line) || line > int(rng.End.Line) {
				continue
			}

			unionField := kind == syntax.UnionFields
			for _, qualifier := range fieldQualifiers(field, unionField) {
				picked = append(picked, pickedFieldAction{
					offset: pf.AST().TokenPosition(field.TokStart()).Offset,
					code:   fieldQualifierAction(pf, fh, field, qualifier),
				})
			}
		}
	})

	sort.Slice(picked, func(i, j int) bool {
		return picked[i].offset < picked[j].offset
	})

	actions := make([]protocol.CodeAction, len(picked))
	for i, p := range picked {
		actions[i] = p.code
	}

	return actions, nil
}

// fieldQualifiers returns the qualifiers the field does not yet carry, in
// the order "required" before "optional". Union fields never offer
// "required": unions have no required members.
func fieldQualifiers(field *syntax.Field, unionField bool) []syntax.TokenKind {
	switch {
	case field.Req == nil:
		if unionField {
			return []syntax.TokenKind{syntax.TokenOptional}
		}

		return []syntax.TokenKind{syntax.TokenRequired, syntax.TokenOptional}
	case field.Req.Kind == syntax.TokenRequired:
		return []syntax.TokenKind{syntax.TokenOptional}
	default: // field.Req.Kind == syntax.TokenOptional
		if unionField {
			return nil
		}

		return []syntax.TokenKind{syntax.TokenRequired}
	}
}

// fieldQualifierAction builds the action switching the field to qualifier:
// the edit replaces the span from the current qualifier keyword, or the
// type start when the field is unqualified, up to the type start.
func fieldQualifierAction(pf *cache.ParsedFile, fh cache.FileHandle, field *syntax.Field, qualifier syntax.TokenKind) protocol.CodeAction {
	keyword := qualifier.String()

	start := pf.AST().TokenPosition(field.Type.TokStart())
	if field.Req != nil {
		start = pf.AST().TokenPosition(pf.AST().TokenIndex(field.Req))
	}
	end := pf.AST().TokenPosition(field.Type.TokStart())

	return protocol.CodeAction{
		Title: fmt.Sprintf("Make field %s (%s)", keyword, field.Name.Text),
		Kind:  new(protocol.CodeActionKindRefactorRewrite),
		Edit: &protocol.WorkspaceEdit{
			Changes: map[uri.URI][]protocol.TextEdit{
				fh.URI(): {{
					Range:   toLSPRange(pf, start, end),
					NewText: keyword + " ",
				}},
			},
		},
	}
}
