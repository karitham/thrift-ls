package lsp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
)

func TestLSPSettings(t *testing.T) {
	t.Run("parses options and drops the path key", func(t *testing.T) {
		patch, err := lspSettings([]byte(`{"path":"/usr/bin/thrift-ls","printWidth":30,"align":"assign"}`))
		require.NoError(t, err)
		require.NotNil(t, patch.PrintWidth)
		assert.Equal(t, 30, *patch.PrintWidth)
		assert.Equal(t, "assign", *patch.Align)
	})

	t.Run("rejects unknown keys", func(t *testing.T) {
		_, err := lspSettings([]byte(`{"printWidth":30,"typoKey":1}`))
		assert.Error(t, err)
	})

	t.Run("rejects invalid values", func(t *testing.T) {
		_, err := lspSettings([]byte(`{"align":"bogus"}`))
		assert.Error(t, err)
	})
}

func TestWorkspaceSettings(t *testing.T) {
	const file = "file:///tmp/settings.thrift"
	content := "struct LongName{1: string fieldNameThatIsQuiteLong}\n"

	ctx := t.Context()
	srv := newMemServer(nil)

	require.NoError(t, srv.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        file,
			LanguageID: "thrift",
			Version:    0,
			Text:       content,
		},
	}))

	format := func() string {
		edits, err := srv.Formatting(ctx, &protocol.DocumentFormattingParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: file},
		})
		require.NoError(t, err)
		require.Len(t, edits, 1)
		return edits[0].NewText
	}

	// Default width 80 keeps the struct on one line.
	assert.Equal(t, "struct LongName { 1: string fieldNameThatIsQuiteLong }\n", format())

	// initializationOptions overlay the base configuration: width 30 breaks it.
	_, err := srv.Initialize(ctx, &protocol.InitializeParams{
		InitializationOptions: protocol.LSPAny([]byte(`{"printWidth":30}`)),
	})
	require.NoError(t, err)
	broken := format()
	assert.Equal(t, "struct LongName {\n    1: string fieldNameThatIsQuiteLong\n}\n", broken)

	// didChangeConfiguration replaces the overlay: width 80 folds again.
	require.NoError(t, srv.DidChangeConfiguration(ctx, &protocol.DidChangeConfigurationParams{
		Settings: protocol.LSPAny([]byte(`{"printWidth":80}`)),
	}))
	assert.Equal(t, "struct LongName { 1: string fieldNameThatIsQuiteLong }\n", format())

	// Invalid settings are rejected and leave the previous ones in effect.
	require.NoError(t, srv.DidChangeConfiguration(ctx, &protocol.DidChangeConfigurationParams{
		Settings: protocol.LSPAny([]byte(`{"printWidth":30,"align":"bogus"}`)),
	}))
	assert.Equal(t, "struct LongName { 1: string fieldNameThatIsQuiteLong }\n", format())
}
