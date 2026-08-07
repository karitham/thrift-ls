package lsp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/formatter"
	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/memoize"
)

func TestServerRangeFormatting(t *testing.T) {
	ctx := t.Context()
	fileURI, err := uri.Parse("file:///tmp/range.thrift")
	assert.NoError(t, err)

	// Only struct B needs reformatting; A and C are already formatted.
	content := `struct A { 1: string a }

struct B {
2: i32 b
}

struct C { 3: i64 c }
`

	store := &memoize.Store{}
	srv := NewServer(cache.New(store, nil), nil, formatter.Options{})

	err = srv.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        fileURI,
			LanguageID: "thrift",
			Version:    0,
			Text:       content,
		},
	})
	assert.NoError(t, err)

	formatting := func() string {
		edits, err := srv.Formatting(ctx, &protocol.DocumentFormattingParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: fileURI},
		})
		assert.NoError(t, err)
		assert.Len(t, edits, 1)

		return edits[0].NewText
	}

	t.Run("formats struct bounded by blank lines", func(t *testing.T) {
		// Select struct B entirely (lines 2-4).
		edits, err := srv.RangeFormatting(ctx, &protocol.DocumentRangeFormattingParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: fileURI},
			Range: protocol.Range{
				Start: protocol.Position{Line: 2, Character: 0},
				End:   protocol.Position{Line: 4, Character: 1},
			},
		})
		assert.NoError(t, err)
		assert.Len(t, edits, 1)

		// The edit covers exactly struct B's lines and collapses it.
		assert.Equal(t, protocol.Range{
			Start: protocol.Position{Line: 2, Character: 0},
			End:   protocol.Position{Line: 4, Character: 1},
		}, edits[0].Range)
		assert.Equal(t, "struct B { 2: i32 b }", edits[0].NewText)

		// Applying the edit reproduces the whole-document formatting.
		want := formatting()
		spliced := content[:strings.Index(content, "struct B")] + edits[0].NewText + content[strings.Index(content, "}\n\nstruct C")+1:]
		assert.Equal(t, want, spliced)
	})

	t.Run("whole document range delegates to full formatting", func(t *testing.T) {
		edits, err := srv.RangeFormatting(ctx, &protocol.DocumentRangeFormattingParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: fileURI},
			Range: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: 7, Character: 0},
			},
		})
		assert.NoError(t, err)
		assert.Len(t, edits, 1)
		assert.Equal(t, formatting(), edits[0].NewText)
	})

	t.Run("no edits for a selection that cuts a declaration", func(t *testing.T) {
		// Start inside struct B's opening line, end inside its field line:
		// the expanded range is not bounded by blank lines.
		edits, err := srv.RangeFormatting(ctx, &protocol.DocumentRangeFormattingParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: fileURI},
			Range: protocol.Range{
				Start: protocol.Position{Line: 2, Character: 5},
				End:   protocol.Position{Line: 3, Character: 4},
			},
		})
		assert.NoError(t, err)
		assert.Empty(t, edits)
	})
}
