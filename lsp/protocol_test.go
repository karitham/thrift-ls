package lsp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karitham/thrift-ls/lsp/cache"
)

func TestInitializeReportsConfiguredVersion(t *testing.T) {
	for _, tt := range []struct {
		name    string
		version string
		want    string
	}{
		{name: "configured", version: "tbuild-test-version", want: "tbuild-test-version"},
		{name: "fallback", want: ServerVersion},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := NewServer(nil, Options{Files: cache.NewMemFS(nil), Version: tt.version})
			result, err := srv.Initialize(t.Context(), testInitializeParams(nil))
			require.NoError(t, err)
			version, ok := result.ServerInfo.Version.Get()
			require.True(t, ok)
			assert.Equal(t, tt.want, version)
		})
	}
}
