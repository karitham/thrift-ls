package analyzers

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/sema"
	"github.com/karitham/thrift-ls/store"
	"github.com/karitham/thrift-ls/syntax"
)

// EnumValuesProvider offers the rewrite that appends an explicit value to
// every member of the enum containing the selection, mirroring the
// auto-incremented constants the compiler would assign. When a diagnostic
// overlaps the selection, the same action is also offered as its quickfix.
type EnumValuesProvider struct{}

func (p EnumValuesProvider) Actions(ctx context.Context, f sema.File, span sema.Span, report sema.Report) []sema.Action {
	// A file with parse errors is not safely editable.
	if len(f.PF.Errors()) > 0 {
		return nil
	}

	enum := enumAt(f.PF, span)
	if enum == nil {
		return nil
	}

	edits, ok := enumValueEdits(f.PF, enum)
	if !ok || len(edits) == 0 {
		return nil
	}

	act := sema.Action{
		Title: "Make enum values explicit",
		File:  f.URI,
		Edits: edits,
	}

	var out []sema.Action

	// A diagnostic on the selection makes the action a quickfix for it;
	// the rewrite stays for kind-filtered requests.
	for _, d := range report[f.URI] {
		if d.Span.Overlaps(span) {
			out = append(out, sema.Action{Title: act.Title, Fix: true, File: f.URI, Edits: edits})

			break
		}
	}

	return append(out, act)
}

// enumAt returns the enum declaration containing the selection start, or
// nil when it lies outside every enum.
func enumAt(pf *store.ParsedFile, span sema.Span) *syntax.Enum {
	for _, enum := range pf.AST().Enums() {
		if pf.AST().Contains(enum, span.Start) {
			return enum
		}
	}

	return nil
}

// enumValueEdits appends " = N" to every member without an explicit value.
// ok is false when the implicit values cannot be computed; the caller must
// then not edit the enum, as the inserted values would be wrong.
func enumValueEdits(pf *store.ParsedFile, enum *syntax.Enum) (edits []sema.Edit, ok bool) {
	for _, im := range sema.EnumImplicitValues(enum) {
		if !im.Known {
			return nil, false
		}

		insertAt := pf.AST().TokenEndPosition(im.Member.Name.TokStart())

		edits = append(edits, sema.Edit{
			Span:    sema.Span{Start: insertAt, End: insertAt},
			NewText: " = " + strconv.FormatInt(im.Value, 10),
		})
	}

	return edits, true
}

// FieldQualifierProvider offers the "Make field required" and "Make field
// optional" actions for the fields whose declaration line the selection
// covers. Every covered field yields one action per qualifier it does not
// already carry: an unqualified field gets both, a required field gets
// "Make field optional", and vice versa. Union fields never offer "Make
// field required": unions have no required members.
type FieldQualifierProvider struct{}

func (p FieldQualifierProvider) Actions(ctx context.Context, f sema.File, span sema.Span, report sema.Report) []sema.Action {
	pf := f.PF

	// A file with parse errors is not safely editable.
	if len(pf.Errors()) > 0 {
		return nil
	}

	// picked pairs an action with the declaration offset of its field, so
	// the actions can be ordered into document order.
	type picked struct {
		offset int
		action sema.Action
	}

	var out []picked

	pf.AST().WalkFieldLists(func(fields []*syntax.Field, kind syntax.FieldListKind) {
		for _, field := range fields {
			line := pf.AST().TokenPosition(field.TokStart()).Line
			if line < span.Start.Line || line > span.End.Line {
				continue
			}

			unionField := kind == syntax.UnionFields
			for _, qualifier := range fieldQualifiers(field, unionField) {
				out = append(out, picked{
					offset: pf.AST().TokenPosition(field.TokStart()).Offset,
					action: fieldQualifierAction(pf, f.URI, field, qualifier),
				})
			}
		}
	})

	sort.Slice(out, func(i, j int) bool {
		return out[i].offset < out[j].offset
	})

	actions := make([]sema.Action, len(out))
	for i, p := range out {
		actions[i] = p.action
	}

	return actions
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
func fieldQualifierAction(pf *store.ParsedFile, file uri.URI, field *syntax.Field, qualifier syntax.TokenKind) sema.Action {
	keyword := qualifier.String()

	start := pf.AST().TokenPosition(field.Type.TokStart())
	if field.Req != nil {
		start = pf.AST().TokenPosition(pf.AST().TokenIndex(field.Req))
	}
	end := pf.AST().TokenPosition(field.Type.TokStart())

	return sema.Action{
		Title: fmt.Sprintf("Make field %s (%s)", keyword, field.Name.Text),
		File:  file,
		Edits: []sema.Edit{{
			Span:    sema.Span{Start: start, End: end},
			NewText: keyword + " ",
		}},
	}
}
