package lsp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

func Test_CodeAction(t *testing.T) {
	ctx := t.Context()
	fileURI, err := uri.Parse("file:///tmp/user.thrift")
	require.NoError(t, err)

	tests := []struct {
		name    string
		content string
		context protocol.CodeActionContext
		want    map[string]protocol.CodeActionKind // title -> kind
	}{
		{
			// Already formatted: only the enum rewrite applies.
			name:    "enum refactor",
			content: "enum E { A, B = 1 }\n",
			want: map[string]protocol.CodeActionKind{
				"Make enum values explicit": protocol.CodeActionKindRefactorRewrite,
			},
		},
		{
			// A reported diagnostic turns the enum refactor into the
			// quickfix for it.
			name:    "enum quickfix",
			content: "enum E { A, B = 1 }\n",
			context: protocol.CodeActionContext{Diagnostics: []protocol.Diagnostic{{
				Range:   protocol.Range{Start: protocol.Position{Character: 10}, End: protocol.Position{Character: 11}},
				Message: protocol.String("A has no explicit value (implicitly 0)"),
			}}},
			want: map[string]protocol.CodeActionKind{
				"Make enum values explicit": protocol.CodeActionKindQuickFix,
			},
		},
		{
			name:    "only quickfix",
			content: "enum E { A, B = 1 }\n",
			context: protocol.CodeActionContext{
				Diagnostics: []protocol.Diagnostic{{
					Range:   protocol.Range{Start: protocol.Position{Character: 10}, End: protocol.Position{Character: 11}},
					Message: protocol.String("A has no enum value (implicitly 0)"),
				}},
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
				Diagnostics: []protocol.Diagnostic{{
					Range:   protocol.Range{Start: protocol.Position{Character: 10}, End: protocol.Position{Character: 11}},
					Message: protocol.String("A has no enum value (implicitly 0)"),
				}},
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(nil)

			err := srv.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{
				TextDocument: protocol.TextDocumentItem{
					URI:        fileURI,
					LanguageID: "thrift",
					Version:    0,
					Text:       tt.content,
				},
			})
			require.NoError(t, err)

			params := &protocol.CodeActionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: fileURI},
				Range:        protocol.Range{Start: protocol.Position{Line: 0, Character: 10}, End: protocol.Position{Line: 0, Character: 10}},
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
