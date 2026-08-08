package lsp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/source"
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
				Code:    protocol.String(source.CodeImplicitEnumValue),
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
					Code:    protocol.String(source.CodeImplicitEnumValue),
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
					Code:    protocol.String(source.CodeImplicitEnumValue),
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
		{
			// An unused include warning offers the removal quickfix on
			// the include line.
			name:    "remove unused include quickfix",
			content: "include \"shared.thrift\"\nstruct S { 1: i32 a }\n",
			context: protocol.CodeActionContext{
				Diagnostics: []protocol.Diagnostic{{
					Range:   protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 0, Character: 22}},
					Code:    protocol.String(source.CodeUnusedInclude),
					Message: protocol.String(`unused include "shared.thrift"`),
				}},
			},
			want: map[string]protocol.CodeActionKind{
				`Remove unused include "shared.thrift"`: protocol.CodeActionKindQuickFix,
			},
		},
		{
			// The same diagnostic elsewhere does not offer the removal.
			name:    "unused include diagnostic elsewhere",
			content: "include \"shared.thrift\"\nstruct S { 1: i32 a }\n",
			context: protocol.CodeActionContext{
				Diagnostics: []protocol.Diagnostic{{
					Range:   protocol.Range{Start: protocol.Position{Line: 5, Character: 0}, End: protocol.Position{Line: 5, Character: 1}},
					Code:    protocol.String(source.CodeUnusedInclude),
					Message: protocol.String(`unused include "shared.thrift"`),
				}},
			},
			want: map[string]protocol.CodeActionKind{},
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

func TestCodeActionAddMissingInclude(t *testing.T) {
	const content = "struct S {\n  1: User u,\n}\n"
	diagnosticRange := protocol.Range{
		Start: protocol.Position{Line: 1, Character: 5},
		End:   protocol.Position{Line: 1, Character: 9},
	}

	tests := []struct {
		name          string
		requestRange  protocol.Range
		diagnostic    protocol.Diagnostic
		wantAddAction bool
	}{
		{
			name: "selection starts before diagnostic",
			requestRange: protocol.Range{
				Start: protocol.Position{Line: 1, Character: 2},
				End:   protocol.Position{Line: 1, Character: 9},
			},
			diagnostic: protocol.Diagnostic{
				Range:   diagnosticRange,
				Code:    protocol.String(source.CodeUndefinedType),
				Message: protocol.String("field type doesn't exist"),
			},
			wantAddAction: true,
		},
		{
			name:         "wrong diagnostic code",
			requestRange: diagnosticRange,
			diagnostic: protocol.Diagnostic{
				Range:   diagnosticRange,
				Code:    protocol.String(source.CodeUndefinedValue),
				Message: protocol.String("default value doesn't exist"),
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

			actions, err := srv.codeAction(t.Context(), &protocol.CodeActionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: fileURI},
				Range:        tt.requestRange,
				Context: protocol.CodeActionContext{
					Diagnostics: []protocol.Diagnostic{tt.diagnostic},
					Only:        []protocol.CodeActionKind{protocol.CodeActionKindQuickFix},
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
