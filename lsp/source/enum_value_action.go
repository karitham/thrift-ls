package source

import (
	"context"
	"strconv"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

// MakeEnumValuesExplicitAction returns the code action that appends an
// explicit value to every member of the enum under rng, mirroring the
// auto-incremented constants the compiler would assign: 0 for the first
// member, one greater than the preceding member's value otherwise.
//
// It returns nil when the selection is outside every enum, the enum is
// already fully explicit, the implicit values cannot be computed (an
// unparseable explicit constant), or the document has parse errors.
func MakeEnumValuesExplicitAction(ctx context.Context, ss *cache.Snapshot, fh cache.FileHandle, rng protocol.Range) (*protocol.CodeAction, error) {
	pf, err := ss.Parse(ctx, fh.URI())
	if err != nil {
		return nil, err
	}

	if pf.AST() == nil || len(pf.Errors()) > 0 {
		return nil, nil
	}

	enum := enumAt(pf, rng)
	if enum == nil {
		return nil, nil
	}

	edits, ok := enumValueEdits(pf, enum)
	if !ok || len(edits) == 0 {
		return nil, nil
	}

	return &protocol.CodeAction{
		Title: "Make enum values explicit",
		Kind:  new(protocol.CodeActionKindRefactorRewrite),
		Edit: &protocol.WorkspaceEdit{
			Changes: map[uri.URI][]protocol.TextEdit{
				fh.URI(): edits,
			},
		},
	}, nil
}

// enumAt returns the enum declaration containing the selection start, or
// nil when it lies outside every enum.
func enumAt(pf *cache.ParsedFile, rng protocol.Range) *syntax.Enum {
	pos, err := pf.Mapper().LSPPosToParserPosition(lspPosition(rng.Start))
	if err != nil {
		return nil
	}

	for _, enum := range pf.AST().Enums() {
		if pf.AST().Contains(enum, pos) {
			return enum
		}
	}

	return nil
}

// enumValueEdits appends " = N" to every member without an explicit value.
// ok is false when the implicit values cannot be computed; the caller must
// then not edit the enum, as the inserted values would be wrong.
func enumValueEdits(pf *cache.ParsedFile, enum *syntax.Enum) (edits []protocol.TextEdit, ok bool) {
	for _, im := range enumImplicitValues(enum) {
		if !im.known {
			return nil, false
		}

		insertAt := pf.AST().TokenEndPosition(im.member.Name.TokStart())

		edits = append(edits, protocol.TextEdit{
			Range:   toLSPRange(pf, insertAt, insertAt),
			NewText: " = " + strconv.FormatInt(im.value, 10),
		})
	}

	return edits, true
}
