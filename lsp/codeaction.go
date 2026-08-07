package lsp

import (
	"context"

	"go.lsp.dev/protocol"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/formatter"
)

// formatDocumentAction is the source.fixAll code action that formats the
// document, mirroring the formatting request.
// codeAction returns the quickfixes for the document: formatting the whole
// document when the range covers it, or the range when it is a selection.
func (s *Server) codeAction(ctx context.Context, params *protocol.CodeActionParams) ([]protocol.CommandOrCodeAction, error) {
	file := params.TextDocument.URI

	view, err := s.session.ViewOf(file)
	if err != nil {
		return nil, err
	}

	ss, release := view.Snapshot()
	defer release()

	fh, err := ss.ReadFile(ctx, file)
	if err != nil {
		return nil, err
	}

	content, err := fh.Content()
	if err != nil {
		return nil, err
	}

	pf, err := ss.Parse(ctx, file)
	if err != nil {
		return nil, err
	}

	if pf.AST() == nil {
		return nil, nil
	}

	out, err := formatter.Format(pf.AST(), s.formatOpts)
	if err != nil {
		return nil, err
	}

	if string(content) == out {
		return nil, nil
	}

	// The edit covers the whole document; for a selection the client
	// applies the range intersection.
	edit := protocol.TextEdit{
		Range: protocol.Range{
			Start: protocol.Position{Line: 0, Character: 0},
			End:   endPosition(content),
		},
		NewText: out,
	}

	action := &protocol.CodeAction{
		Title: "Format document",
		Kind:  new(protocol.CodeActionKindSourceFixAll),
		Edit: &protocol.WorkspaceEdit{
			Changes: map[uri.URI][]protocol.TextEdit{
				file: {edit},
			},
		},
	}

	return []protocol.CommandOrCodeAction{action}, nil
}

// endPosition returns the position after the last byte of content.
func endPosition(content []byte) protocol.Position {
	line, char := uint32(0), uint32(0)

	for _, b := range content {
		if b == '\n' {
			line++
			char = 0

			continue
		}

		char++
	}

	return protocol.Position{Line: line, Character: char}
}
