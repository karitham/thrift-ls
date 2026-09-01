package lsp

import (
	"context"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/sema"
)

// codeAction returns the code actions for the document: quickfixes for the
// diagnostics the server published for it (inline fixes, then fixers),
// then the refactor actions for the code at the selection. An action that
// fixes a reported diagnostic is also offered as a quickfix. Actions are
// filtered to the kinds the client requested.
func (s *Server) codeAction(ctx context.Context, params *protocol.CodeActionParams) ([]protocol.CommandOrCodeAction, error) {
	return withFile(ctx, s.session, params.TextDocument.URI, func(view *cache.View, fh cache.FileHandle) ([]protocol.CommandOrCodeAction, error) {
		pf, err := view.Parse(ctx, params.TextDocument.URI)
		if err != nil {
			return nil, err
		}

		span, err := toSemaSpan(pf, params.Range)
		if err != nil {
			return nil, err
		}

		report := s.reportFor(params.TextDocument.URI)

		actions := sema.DefaultPipeline(s.lintConfig(view)).
			CodeActions(ctx, view, params.TextDocument.URI, span, report)

		proto := make([]protocol.CodeAction, 0, len(actions))
		for _, a := range actions {
			proto = append(proto, toProtocolCodeAction(pf, a))
		}

		proto = preferQuickFixes(filterCodeActions(proto, params.Context.Only))

		out := make([]protocol.CommandOrCodeAction, 0, len(proto))
		for i := range proto {
			out = append(out, &proto[i])
		}

		return out, nil
	})
}

// toSemaSpan converts an LSP range to the pipeline's parser-coordinate
// span through the file's mapper.
func toSemaSpan(pf *cache.ParsedFile, rng protocol.Range) (sema.Span, error) {
	m := pf.Mapper()

	start, err := m.LSPPosToParserPosition(rng.Start)
	if err != nil {
		return sema.Span{}, err
	}

	end, err := m.LSPPosToParserPosition(rng.End)
	if err != nil {
		return sema.Span{}, err
	}

	return sema.Span{Start: start, End: end}, nil
}

// toProtocolCodeAction translates a pipeline action to the wire type.
func toProtocolCodeAction(pf *cache.ParsedFile, a sema.Action) protocol.CodeAction {
	kind := protocol.CodeActionKindRefactorRewrite
	if a.Fix {
		kind = protocol.CodeActionKindQuickFix
	}

	changes := make(map[uri.URI][]protocol.TextEdit, 1)
	edits := make([]protocol.TextEdit, 0, len(a.Edits))

	for _, e := range a.Edits {
		// Edit spans carry authoritative byte offsets; the mapper turns
		// them into UTF-16 columns the wire requires.
		start, err := pf.Mapper().OffsetToLSPPosition(e.Span.Start.Offset)
		if err != nil {
			start = protocol.Position{Line: uint32(e.Span.Start.Line - 1), Character: uint32(e.Span.Start.Col - 1)}
		}

		end, err := pf.Mapper().OffsetToLSPPosition(e.Span.End.Offset)
		if err != nil {
			end = protocol.Position{Line: uint32(e.Span.End.Line - 1), Character: uint32(e.Span.End.Col - 1)}
		}

		edits = append(edits, protocol.TextEdit{
			Range:   protocol.Range{Start: start, End: end},
			NewText: e.NewText,
		})
	}

	changes[a.File] = edits

	return protocol.CodeAction{
		Title: a.Title,
		Kind:  &kind,
		Edit:  &protocol.WorkspaceEdit{Changes: changes},
	}
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
