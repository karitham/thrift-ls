package lsp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/resolver/resolvertest"
)

// TestCodeAction folds every built-in quickfix/refactor case into one
// table: enum rewrites and quickfixes with kind filtering, unused-include
// removal, and selections that offer nothing.
func TestCodeAction(t *testing.T) {
	fileURI := uri.URI("file:///tmp/user.thrift")

	for _, tt := range []struct {
		name    string
		content string
		rng     protocol.Range
		context protocol.CodeActionContext
		want    map[string]protocol.CodeActionKind
	}{
		{
			name:    "enum rewrite away from diagnostics",
			content: "enum E { A, B = 1 }\n",
			rng: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 18},
				End:   protocol.Position{Line: 0, Character: 18},
			},
			want: map[string]protocol.CodeActionKind{
				"Make enum values explicit": protocol.CodeActionKindRefactorRewrite,
			},
		},
		{
			name:    "enum quickfix",
			content: "enum E { A, B = 1 }\n",
			want: map[string]protocol.CodeActionKind{
				"Add explicit value 0 to A": protocol.CodeActionKindQuickFix,
				"Make enum values explicit": protocol.CodeActionKindQuickFix,
			},
		},
		{
			name:    "only quickfix",
			content: "enum E { A, B = 1 }\n",
			context: protocol.CodeActionContext{
				Only: []protocol.CodeActionKind{protocol.CodeActionKindQuickFix},
			},
			want: map[string]protocol.CodeActionKind{
				"Add explicit value 0 to A": protocol.CodeActionKindQuickFix,
				"Make enum values explicit": protocol.CodeActionKindQuickFix,
			},
		},
		{
			name:    "only refactor drops the quickfix",
			content: "enum E { A, B = 1 }\n",
			context: protocol.CodeActionContext{
				Only: []protocol.CodeActionKind{protocol.CodeActionKindRefactorRewrite},
			},
			want: map[string]protocol.CodeActionKind{
				"Make enum values explicit": protocol.CodeActionKindRefactorRewrite,
			},
		},
		{
			name:    "no applicable actions",
			content: "enum E { A = 1, B = 2 }\n",
			want:    map[string]protocol.CodeActionKind{},
		},
		{
			name:    "remove unused include quickfix",
			content: "include \"shared.thrift\"\nstruct S { 1: i32 a }\n",
			want: map[string]protocol.CodeActionKind{
				`Remove unused include "shared.thrift"`: protocol.CodeActionKindQuickFix,
			},
		},
		{
			name:    "selection away from the diagnostic",
			content: "include \"shared.thrift\"\nstruct S {}\n",
			rng: protocol.Range{
				Start: protocol.Position{Line: 1, Character: 5},
				End:   protocol.Position{Line: 1, Character: 6},
			},
			want: map[string]protocol.CodeActionKind{},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			srv := newMemServer(nil)

			require.NoError(t, srv.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{
				TextDocument: protocol.TextDocumentItem{
					URI:        fileURI,
					LanguageID: "thrift",
					Version:    0,
					Text:       tt.content,
				},
			}))
			diagnosePair(t, srv, fileURI)

			rng := tt.rng
			if rng == (protocol.Range{}) {
				rng = protocol.Range{
					Start: protocol.Position{Line: 0, Character: 10},
					End:   protocol.Position{Line: 0, Character: 10},
				}
			}

			assert.Equal(t, tt.want, codeActionTitles(t, srv, fileURI, rng, tt.context.Only...))
		})
	}
}

func TestCodeActionAddMissingInclude(t *testing.T) {
	const content = "struct S {\n  1: User u,\n}\n"

	for _, tt := range []struct {
		name          string
		requestRange  protocol.Range
		wantAddAction bool
	}{
		{
			name: "selection overlaps the undefined type",
			requestRange: protocol.Range{
				Start: protocol.Position{Line: 1, Character: 2},
				End:   protocol.Position{Line: 1, Character: 9},
			},
			wantAddAction: true,
		},
		{
			name: "selection elsewhere offers no fix",
			requestRange: protocol.Range{
				Start: protocol.Position{Line: 2, Character: 0},
				End:   protocol.Position{Line: 2, Character: 1},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			files := resolvertest.Map{
				"/ws/shared.thrift": []byte("struct User { 1: i32 id }\n"),
			}.URIs()

			srv := newSyncServerWithOptions(nil, files, Options{})
			fileURI := uri.File("/ws/user.thrift")
			openDocument(t, srv, fileURI, content)
			diagnosePair(t, srv, fileURI)

			actions, err := srv.codeAction(t.Context(), &protocol.CodeActionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: fileURI},
				Range:        tt.requestRange,
				Context: protocol.CodeActionContext{
					Only: []protocol.CodeActionKind{protocol.CodeActionKindQuickFix},
				},
			})
			require.NoError(t, err)

			var found *protocol.CodeAction
			for _, action := range actions {
				codeAction, ok := action.(*protocol.CodeAction)
				if ok && codeAction.Title == `Add include "shared.thrift"` {
					found = codeAction
				}
			}

			if tt.wantAddAction {
				require.NotNil(t, found)

				return
			}

			assert.Nil(t, found)
		})
	}
}
