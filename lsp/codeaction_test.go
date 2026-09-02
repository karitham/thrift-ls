package lsp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
)

func Test_CodeAction(t *testing.T) {
	ctx := t.Context()
	fileURI, err := uri.Parse("file:///tmp/user.thrift")
	require.NoError(t, err)

	tests := []struct {
		name    string
		content string
		rng     protocol.Range // zero: a cursor at line 0, character 10
		context protocol.CodeActionContext
		want    map[string]protocol.CodeActionKind // title -> kind
	}{
		{
			// The server's own diagnostics turn the rewrite into the
			// quickfix for them; a selection away from every diagnostic
			// keeps the plain rewrite.
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
			// The server's own reported diagnostic turns the enum
			// refactor into the quickfix for it.
			name:    "enum quickfix",
			content: "enum E { A, B = 1 }\n",
			want: map[string]protocol.CodeActionKind{
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
			// An unused include warning offers the removal quickfix on
			// the include line.
			name:    "remove unused include quickfix",
			content: "include \"shared.thrift\"\nstruct S { 1: i32 a }\n",
			want: map[string]protocol.CodeActionKind{
				`Remove unused include "shared.thrift"`: protocol.CodeActionKindQuickFix,
			},
		},
		{
			// A selection away from the diagnostic offers nothing.
			name:    "selection away from the diagnostic",
			content: "include \"shared.thrift\"\nstruct S {}\n",
			rng: protocol.Range{
				Start: protocol.Position{Line: 1, Character: 5},
				End:   protocol.Position{Line: 1, Character: 6},
			},
			want: map[string]protocol.CodeActionKind{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newMemServer(nil)

			err := srv.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{
				TextDocument: protocol.TextDocumentItem{
					URI:        fileURI,
					LanguageID: "thrift",
					Version:    0,
					Text:       tt.content,
				},
			})
			require.NoError(t, err)

			// Produce the report the server would publish, so code
			// actions pair with the server's own diagnostics.
			_, err = withFile(ctx, srv.session.ViewOf, fileURI, func(view *cache.View, _ cache.FileHandle) (struct{}, error) {
				srv.diagnose(ctx, view, []uri.URI{fileURI})
				return struct{}{}, nil
			})
			require.NoError(t, err)

			rng := tt.rng
			if rng == (protocol.Range{}) {
				rng = protocol.Range{
					Start: protocol.Position{Line: 0, Character: 10},
					End:   protocol.Position{Line: 0, Character: 10},
				}
			}

			params := &protocol.CodeActionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: fileURI},
				Range:        rng,
				Context:      tt.context,
			}

			actions, err := srv.codeAction(ctx, params)
			require.NoError(t, err)

			got := make(map[string]protocol.CodeActionKind)
			for _, a := range actions {
				ca, ok := a.(*protocol.CodeAction)
				require.True(t, ok, "expected a code action, got %T", a)
				require.NotNil(t, ca.Kind)
				got[ca.Title] = *ca.Kind
			}

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCodeActionAddMissingInclude(t *testing.T) {
	const content = "struct S {\n  1: User u,\n}\n"

	tests := []struct {
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			shared := filepath.Join(dir, "shared.thrift")
			user := filepath.Join(dir, "user.thrift")
			require.NoError(t, os.WriteFile(shared, []byte("struct User { 1: i32 id }\n"), 0o644))
			require.NoError(t, os.WriteFile(user, []byte(content), 0o644))

			srv := newTestServer(nil)
			fileURI := uri.File(user)
			openDocument(t, srv, fileURI, content)

			// Produce the report the server would publish.
			_, err := withFile(t.Context(), srv.session.ViewOf, fileURI, func(view *cache.View, _ cache.FileHandle) (struct{}, error) {
				srv.diagnose(t.Context(), view, []uri.URI{fileURI})
				return struct{}{}, nil
			})
			require.NoError(t, err)

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
