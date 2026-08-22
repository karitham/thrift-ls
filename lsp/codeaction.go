package lsp

import (
	"context"
	"strings"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/source"
)

// codeAction returns the code actions for the document: the refactors for
// the code at the selection. An action that fixes a reported diagnostic is
// also offered as a quickfix. Actions are filtered to the kinds the client
// requested.
func (s *Server) codeAction(ctx context.Context, params *protocol.CodeActionParams) ([]protocol.CommandOrCodeAction, error) {
	return withFile(ctx, s.session, params.TextDocument.URI, func(view *cache.View, fh cache.FileHandle) ([]protocol.CommandOrCodeAction, error) {
		var actions []protocol.CodeAction

		enum, err := source.MakeEnumValuesExplicitAction(ctx, view, fh, params.Range)
		if err != nil {
			return nil, err
		}
		if enum != nil {
			// A diagnostic on the selection makes the action a quickfix
			// for it; the rewrite stays for kind-filtered requests.
			if diagnosticOverlaps(params.Context.Diagnostics, params.Range) {
				fix := *enum
				fix.Kind = new(protocol.CodeActionKindQuickFix)
				actions = append(actions, fix)
			}
			actions = append(actions, *enum)
		}

		fieldActions, err := source.MakeFieldQualifierAction(ctx, view, fh, params.Range)
		if err != nil {
			return nil, err
		}
		actions = append(actions, fieldActions...)

		removeInclude, err := source.MakeRemoveUnusedIncludeAction(ctx, view, fh, params.Range, params.Context.Diagnostics)
		if err != nil {
			return nil, err
		}
		if removeInclude != nil {
			actions = append(actions, *removeInclude)
		}

		addInclude, err := source.MakeAddMissingIncludeAction(ctx, view, fh, params.Range, params.Context.Diagnostics)
		if err != nil {
			return nil, err
		}
		if addInclude != nil {
			actions = append(actions, *addInclude)
		}

		actions = preferQuickFixes(filterCodeActions(actions, params.Context.Only))

		out := make([]protocol.CommandOrCodeAction, 0, len(actions))
		for i := range actions {
			out = append(out, &actions[i])
		}
		return out, nil
	})
}

// diagnosticOverlaps reports whether any diagnostic shares a position with
// rng: the client presents a problem there, so the action is a quickfix for
// it.
func diagnosticOverlaps(diags []protocol.Diagnostic, rng protocol.Range) bool {
	for _, d := range diags {
		if source.RangesOverlap(rng, d.Range) {
			return true
		}
	}

	return false
}

// filterCodeActions keeps only the actions whose kind falls under one of
// the requested kinds. An empty request keeps everything.
func filterCodeActions(actions []protocol.CodeAction, kinds []protocol.CodeActionKind) []protocol.CodeAction {
	if len(kinds) == 0 {
		return actions
	}

	var out []protocol.CodeAction

	for _, act := range actions {
		if act.Kind == nil {
			continue
		}

		for _, kind := range kinds {
			if strings.HasPrefix(string(*act.Kind), string(kind)) {
				out = append(out, act)
				break
			}
		}
	}

	return out
}

// preferQuickFixes drops a refactor.rewrite action when a quickfix action
// with the same title is also offered, so clients without kind grouping
// show the action once, as the fix.
func preferQuickFixes(actions []protocol.CodeAction) []protocol.CodeAction {
	quickfix := make(map[string]bool)
	for _, act := range actions {
		if act.Kind != nil && *act.Kind == protocol.CodeActionKindQuickFix {
			quickfix[act.Title] = true
		}
	}

	if len(quickfix) == 0 {
		return actions
	}

	var out []protocol.CodeAction

	for _, act := range actions {
		if act.Kind != nil && *act.Kind == protocol.CodeActionKindRefactorRewrite && quickfix[act.Title] {
			continue
		}
		out = append(out, act)
	}

	return out
}
