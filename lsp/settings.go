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
// formatting with silently stale options. Parse failures are expected
// errors: the document is client input, and the previous settings stay in
// effect.
func lspSettings(data []byte) (*options.Patch, error) {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, Expected(fmt.Errorf("lsp settings: %w", err))
	}
	delete(m, "path")

	clean, err := json.Marshal(m)
	if err != nil {
		return nil, Expected(fmt.Errorf("lsp settings: %w", err))
	}

	p, err := options.Parse(clean)
	if err != nil {
		return nil, Expected(err)
	}

	return p, nil
}
