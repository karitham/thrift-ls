package lsp

import (
	"encoding/json"
	"fmt"

	"github.com/karitham/thrift-ls/options"
)

// lspSettings converts the settings document sent by an LSP client
// (initializationOptions or the settings of didChangeConfiguration) into an
// options patch. The `path` extension setting is not an options key and is
// dropped; unknown keys are rejected so typos fail loudly instead of
// formatting with silently stale options.
func lspSettings(data []byte) (*options.Patch, error) {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("lsp settings: %w", err)
	}
	delete(m, "path")

	clean, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("lsp settings: %w", err)
	}

	return options.Parse(clean)
}
