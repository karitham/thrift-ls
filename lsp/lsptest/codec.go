package lsptest

// codec.go bridges jsonrpc2 payload handling to the protocol package's
// LSP-aware marshalers. Without it, sealed-union result types such as
// DefinitionResult fail to decode: jsonrpc2's default codec knows nothing
// about protocol's custom union unmarshalers.

import (
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
)

// lspWireCodec implements [jsonrpc2.Codec] on top of protocol.Marshal and
// protocol.Unmarshal, mirroring the codec the protocol package installs on
// connections it builds itself.
type lspWireCodec struct{}

func (lspWireCodec) Marshal(v any) ([]byte, error) {
	switch m := v.(type) {
	case jsonrpc2.RawMessage:
		if m == nil {
			return []byte("null"), nil
		}

		return m, nil
	default:
		return protocol.Marshal(v)
	}
}

func (lspWireCodec) Unmarshal(data []byte, v any) error {
	if p, ok := v.(*jsonrpc2.RawMessage); ok {
		b := make(jsonrpc2.RawMessage, len(data))
		copy(b, data)
		*p = b

		return nil
	}

	return protocol.Unmarshal(data, v)
}
