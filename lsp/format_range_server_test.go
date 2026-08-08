package lsp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/mapper"
	"github.com/karitham/thrift-ls/options"
)

func TestServerRangeFormatting(t *testing.T) {
	ctx := t.Context()
	fileURI := uri.URI("file:///tmp/range.thrift")

	// Only struct B needs reformatting; A and C are already formatted.
	content := `struct A { 1: string a }

struct B {
2: i32 b
}

struct C { 3: i64 c }
`

	srv := NewServer(cache.New(nil), nil, options.Patch{})

	require.NoError(t, srv.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        fileURI,
			LanguageID: "thrift",
			Version:    0,
			Text:       content,
		},
	}))

	// applyEdits applies the edits to the given text via the mapper.
	applyEdits := func(text string, edits []protocol.TextEdit) string {
		got, err := mapper.NewMapper([]byte(text)).ApplyEdits(edits)
		require.NoError(t, err)

		return string(got)
	}

	formatting := func() string {
		edits, err := srv.Formatting(ctx, &protocol.DocumentFormattingParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: fileURI},
		})
		require.NoError(t, err)
		require.Len(t, edits, 1)

		return edits[0].NewText
	}

	t.Run("formats the enclosing block of the selection", func(t *testing.T) {
		// Select the two lines of struct B's body (lines 3-4).
		edits, err := srv.RangeFormatting(ctx, &protocol.DocumentRangeFormattingParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: fileURI},
			Range: protocol.Range{
				Start: protocol.Position{Line: 3, Character: 0},
				End:   protocol.Position{Line: 4, Character: 0},
			},
		})
		require.NoError(t, err)
		require.Len(t, edits, 1)

		// The edit covers exactly struct B's block and collapses it.
		assert.Equal(t, protocol.Range{
			Start: protocol.Position{Line: 2, Character: 0},
			End:   protocol.Position{Line: 5, Character: 0},
		}, edits[0].Range)
		assert.Equal(t, "struct B { 2: i32 b }\n", edits[0].NewText)

		// Applying the edit reproduces the whole-document formatting.
		want := formatting()
		assert.Equal(t, want, applyEdits(content, edits))
	})

	t.Run("mid-declaration selection still formats the enclosing block", func(t *testing.T) {
		// Select a slice that cuts struct B in half (inside the opening
		// line to inside the field line).
		edits, err := srv.RangeFormatting(ctx, &protocol.DocumentRangeFormattingParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: fileURI},
			Range: protocol.Range{
				Start: protocol.Position{Line: 2, Character: 5},
				End:   protocol.Position{Line: 3, Character: 4},
			},
		})
		require.NoError(t, err)
		require.Len(t, edits, 1)
		assert.Equal(t, "struct B { 2: i32 b }\n", edits[0].NewText)
	})

	t.Run("whole document range returns the full formatting edits", func(t *testing.T) {
		edits, err := srv.RangeFormatting(ctx, &protocol.DocumentRangeFormattingParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: fileURI},
			Range: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: 7, Character: 0},
			},
		})
		require.NoError(t, err)

		assert.Equal(t, formatting(), applyEdits(content, edits))
	})

	t.Run("selection outside any changed block yields no edits", func(t *testing.T) {
		// Struct C is already formatted: selecting it changes nothing.
		edits, err := srv.RangeFormatting(ctx, &protocol.DocumentRangeFormattingParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: fileURI},
			Range: protocol.Range{
				Start: protocol.Position{Line: 6, Character: 0},
				End:   protocol.Position{Line: 6, Character: 20},
			},
		})
		require.NoError(t, err)
		assert.Empty(t, edits)
	})

	t.Run("unsaved overlay content is formatted", func(t *testing.T) {
		// Change the buffer without saving: range formatting must read
		// the overlay, not the disk.
		unsaved := `struct A { 1: string a }

struct D {
4: i64 d
}
`
		require.NoError(t, srv.DidChange(ctx, &protocol.DidChangeTextDocumentParams{
			TextDocument: protocol.VersionedTextDocumentIdentifier{
				TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: fileURI},
				Version:                1,
			},
			ContentChanges: []protocol.TextDocumentContentChangeEvent{
				&protocol.TextDocumentContentChangeWholeDocument{
					Text: unsaved,
				},
			},
		}))

		edits, err := srv.RangeFormatting(ctx, &protocol.DocumentRangeFormattingParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: fileURI},
			Range: protocol.Range{
				Start: protocol.Position{Line: 3, Character: 0},
				End:   protocol.Position{Line: 3, Character: 6},
			},
		})
		require.NoError(t, err)
		require.Len(t, edits, 1)
		assert.Equal(t, "struct D { 4: i64 d }\n", edits[0].NewText)
		assert.Equal(t, "struct A { 1: string a }\n\nstruct D { 4: i64 d }\n", applyEdits(unsaved, edits))
	})
}
