package lsp

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"

	"go.lsp.dev/jsonrpc2"
)

// DebugHandler wraps a jsonrpc2.Handler with request/response debug logging
// and panic recovery. A recovered panic becomes an error response, so the
// client never sees a success for a request that crashed.
func DebugHandler(handler jsonrpc2.Handler) jsonrpc2.Handler {
	return func(ctx context.Context, req *jsonrpc2.Request) (result any, err error) {
		if req != nil {
			slog.Debug("jsonrpc request", "method", req.Method(), "params", string(req.Params()))
		}

		defer func() {
			if r := recover(); r != nil {
				slog.Error("recovered from panic", "panic", r, "stack", string(debug.Stack()))
				err = fmt.Errorf("panic: %v", r)
			}
		}()

		return handler(ctx, req)
	}
}
