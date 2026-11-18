package lsp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/internal/proptest"
	"github.com/karitham/thrift-ls/mapper"
)

var propertyFieldTypes = []string{"i32", "string", "bool", "double"}

// drawUnformattedDocument draws a small IDL document the formatter always
// rewrites: one field per line, which the default width collapses.
func drawUnformattedDocument(t *proptest.T) string {
	name := proptest.SampledFrom([]string{"Alpha", "Beta", "Gamma"}).Draw(t)
	fields := proptest.IntRange(1, 2).Draw(t)

	var doc strings.Builder

	fmt.Fprintf(&doc, "struct %s {\n", name)

	for i := 1; i <= fields; i++ {
		fmt.Fprintf(&doc, "%d: %s f%d\n", i, proptest.SampledFrom(propertyFieldTypes).Draw(t), i)
	}

	doc.WriteString("}\n")

	return doc.String()
}

// FuzzFormattingEditsSameStartInsertion drives the formatting seam with a
// generated document: the formatter's block replacement plus a same-offset
// insertion must produce the insertion's text immediately before the
// replacement, in either argument order.
func FuzzFormattingEditsSameStartInsertion(f *testing.F) {
	fileURI := uri.File("/property/format.thrift")
	insertionText := "// generated\n"

	proptest.Fuzz(f, proptest.Corpus{
		[]byte(""),
		[]byte("alpha"),
		[]byte("one field"),
		[]byte("two fields"),
		[]byte("gamma"),
	}, func(t *proptest.T) {
		doc := drawUnformattedDocument(t)

		srv := newMemServer(nil)
		require.NoError(t, srv.DidOpen(context.Background(), &protocol.DidOpenTextDocumentParams{
			TextDocument: protocol.TextDocumentItem{
				URI:        fileURI,
				LanguageID: LanguageIDThrift,
				Version:    0,
				Text:       doc,
			},
		}))

		edits, err := srv.Formatting(context.Background(), &protocol.DocumentFormattingParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: fileURI},
		})
		require.NoError(t, err)
		require.Len(t, edits, 1, "document %q must need reformatting", doc)

		formatting := edits[0]
		content := []byte(doc)

		positionMapper := mapper.NewMapper(content)

		start, err := positionMapper.LSPPosToParserPosition(formatting.Range.Start)
		require.NoError(t, err)

		end, err := positionMapper.LSPPosToParserPosition(formatting.Range.End)
		require.NoError(t, err)

		// Applying the formatter's edit alone reproduces the formatted text.
		alone, err := mapper.NewMapper(content).ApplyEdits(edits)
		require.NoError(t, err)
		require.Equal(t, doc[:start.Offset]+formatting.NewText+doc[end.Offset:], string(alone))

		insertion := protocol.TextEdit{Range: protocol.Range{Start: formatting.Range.Start, End: formatting.Range.Start}, NewText: insertionText}
		want := doc[:start.Offset] + insertionText + formatting.NewText + doc[end.Offset:]

		for _, order := range [][]protocol.TextEdit{
			{insertion, formatting},
			{formatting, insertion},
		} {
			got, err := mapper.NewMapper(content).ApplyEdits(order)
			require.NoError(t, err)
			require.Equal(t, want, string(got), "a same-offset insertion must precede the replacement in either order")
		}
	})
}
