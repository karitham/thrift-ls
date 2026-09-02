package lsp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
)

func TestInitializeReportsConfiguredVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    string
	}{
		{name: "configured", version: "tbuild-test-version", want: "tbuild-test-version"},
		{name: "fallback", want: ServerVersion},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := NewServer(cache.NewMemFS(nil), nil, Options{Version: tt.version})
			result, err := srv.Initialize(t.Context(), testInitializeParams(nil))
			require.NoError(t, err)
			version, ok := result.ServerInfo.Version.Get()
			require.True(t, ok)
			assert.Equal(t, tt.want, version)
		})
	}
}

func testInitializeParams(folders []protocol.WorkspaceFolder) *protocol.InitializeParams {
	params := &protocol.InitializeParams{}
	params.WorkspaceFolders = protocol.NewNullable(folders)

	return params
}

func testCompletionParams(file uri.URI, line, character uint32) *protocol.CompletionParams {
	params := &protocol.CompletionParams{
		Context: protocol.CompletionContext{
			TriggerKind: protocol.CompletionTriggerKindInvoked,
		},
	}
	params.TextDocument = protocol.TextDocumentIdentifier{URI: file}
	params.Position = protocol.Position{Line: line, Character: character}
	params.WorkDoneToken = protocol.String("")
	params.PartialResultToken = protocol.String("")

	return params
}
