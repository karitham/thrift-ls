package lsp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// TestCompletion folds single-file, include-scope, and isolation cases
// into one table: each case opens its files, completes at the cursor, and
// runs its own label assertions.
func TestCompletion(t *testing.T) {
	for _, tt := range []struct {
		name      string
		files     map[string]string
		target    string
		line      uint32
		character uint32
		check     func(t *testing.T, srv *Server, target uri.URI, labels []string)
	}{
		{
			name: "field name at end of line",
			files: map[string]string{
				"file:///tmp/file.thrift": "include \"base.thrift\"\n\nstruct Test {\n\t1: required string Name,\n\t2: optional i32 Age,\n        3: required string N\n}",
			},
			target:    "file:///tmp/file.thrift",
			line:      5,
			character: 28,
			check: func(t *testing.T, srv *Server, target uri.URI, labels []string) {
				t.Helper()
				require.NotEmpty(t, labels)
				assert.Equal(t, "Name", labels[0])

				result, err := srv.Completion(t.Context(), testCompletionParams(target, 5, 28))
				require.NoError(t, err)
				list, ok := result.(*protocol.CompletionList)
				require.True(t, ok)
				require.NotEmpty(t, list.Items)
				assert.LessOrEqual(t, len(list.Items), 10)
				preselect, _ := list.Items[0].Preselect.Get()
				assert.True(t, preselect)
				edit, ok := list.Items[0].TextEdit.(*protocol.TextEdit)
				require.True(t, ok)
				assert.Equal(t, "Name", edit.NewText)
				assert.Equal(t, protocol.Range{
					Start: protocol.Position{Line: 5, Character: 27},
					End:   protocol.Position{Line: 5, Character: 28},
				}, edit.Range)
			},
		},
		{
			name: "includes enum from included file",
			files: map[string]string{
				"file:///tmp/base.thrift": "enum Name { ONE, TWO }",
				"file:///tmp/test.thrift": "include \"base.thrift\"\n\nstruct Test {\n\t1: required string Name,\n\t2: optional i32 Age,\n        3: required string N\n}",
			},
			target:    "file:///tmp/test.thrift",
			line:      5,
			character: 28,
			check: func(t *testing.T, _ *Server, _ uri.URI, labels []string) {
				t.Helper()
				assert.Contains(t, labels, "Name")
			},
		},
		{
			name: "no global pollution from unrelated files",
			files: map[string]string{
				"file:///tmp/file1.thrift": "include \"base.thrift\"\n\nstruct Test {\n\t1: required string Name,\n\t2: optional i32 Age,\n        3: required string N\n}",
				"file:///tmp/file2.thrift": "include \"other.thrift\"\n\nstruct Other {\n\t1: required string Field2,\n\t2: optional i32 Other,\n        3: required string M\n}",
			},
			target:    "file:///tmp/file1.thrift",
			line:      5,
			character: 28,
			check: func(t *testing.T, _ *Server, _ uri.URI, labels []string) {
				t.Helper()
				assert.NotContains(t, labels, "Field2")
				assert.NotContains(t, labels, "Other")
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			srv := newMemServer(nil)

			for name, content := range tt.files {
				fileURI, err := uri.Parse(name)
				require.NoError(t, err)
				require.NoError(t, srv.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{
					TextDocument: protocol.TextDocumentItem{
						URI:        fileURI,
						LanguageID: "thrift",
						Version:    0,
						Text:       content,
					},
				}))
			}

			target, err := uri.Parse(tt.target)
			require.NoError(t, err)
			tt.check(t, srv, target, completionLabels(t, srv, target, tt.line, tt.character))
		})
	}
}

// TestCompletionQualifiedType pins qualified type completion: in a type
// position, typing an include name followed by a dot suggests the
// include's types, qualified.
func TestCompletionQualifiedType(t *testing.T) {
	ctx := t.Context()

	baseText := "struct MobileSuit {\n\t1: required string Name\n}\n\nstruct Guntank {\n\t1: required i32 Treads\n}"
	testContent := "include \"federation.thrift\"\n\nstruct StrikeRouge {\n\t1: required federation.MobileSuit pack,\n\t2: required federation.Guntank support,\n}"
	testURI := uri.URI("file:///tmp/test.thrift")

	openBoth := func(t *testing.T, content string) *Server {
		t.Helper()
		srv := newMemServer(nil)
		require.NoError(t, srv.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{
			TextDocument: protocol.TextDocumentItem{
				URI:        "file:///tmp/federation.thrift",
				LanguageID: "thrift",
				Version:    0,
				Text:       baseText,
			},
		}))
		require.NoError(t, srv.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{
			TextDocument: protocol.TextDocumentItem{
				URI:        testURI,
				LanguageID: "thrift",
				Version:    0,
				Text:       content,
			},
		}))

		return srv
	}

	t.Run("mid-word qualified prefix suggests matching include types", func(t *testing.T) {
		labels := completionLabels(t, openBoth(t, testContent), testURI, 3, 27)
		assert.Contains(t, labels, "federation.MobileSuit")
		assert.NotContains(t, labels, "federation.Guntank")
		assert.NotContains(t, labels, "MobileSuit")
	})

	t.Run("cursor right after the dot suggests include types", func(t *testing.T) {
		labels := completionLabels(t, openBoth(t, testContent), testURI, 4, 24)
		assert.Contains(t, labels, "federation.Guntank")
		assert.Contains(t, labels, "federation.MobileSuit")
	})

	t.Run("no services in a type slot", func(t *testing.T) {
		content := "include \"federation.thrift\"\n\nstruct StrikeRouge {\n\t1: required |\n}"
		labels := completionLabels(t, openBoth(t, content), testURI, 3, 15)
		assert.Contains(t, labels, "i32")
		assert.NotContains(t, labels, "Federation")
	})
}
