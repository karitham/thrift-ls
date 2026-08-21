package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/jsonrpc2"
)

// rpcFrame is one JSON-RPC message as it crosses the wire, keeping the
// raw fields so responses and notifications are distinguishable.
type rpcFrame struct {
	ID     *json.RawMessage `json:"id,omitempty"`
	Method string           `json:"method,omitempty"`
	Result json.RawMessage  `json:"result,omitempty"`
	Params json.RawMessage  `json:"params,omitempty"`
}

func (f rpcFrame) isRequest() bool { return f.ID != nil && f.Method != "" }
func (f rpcFrame) isResponse() bool {
	return f.ID != nil && f.Method == ""
}
func (f rpcFrame) idInt() int {
	if f.ID == nil {
		return -1
	}

	var n int

	if err := json.Unmarshal(*f.ID, &n); err != nil {
		return -1
	}

	return n
}

// pipeConn is one end of an io.Pipe as a ReadWriteCloser. io.Pipe
// coordinates reads and writes through channels, so goroutines blocked on
// it are visible to testing/synctest (net.Pipe blocks via sync.Cond and
// would hang the bubble).
type pipeConn struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func (c pipeConn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c pipeConn) Write(p []byte) (int, error) { return c.w.Write(p) }

func (c pipeConn) Close() error {
	c.r.Close()
	c.w.Close()

	return nil
}

func pipePair() (pipeConn, pipeConn) {
	ar, bw := io.Pipe()
	br, aw := io.Pipe()

	return pipeConn{ar, aw}, pipeConn{br, bw}
}

// writeFrame serializes v as one Content-Length framed message.
func writeFrame(t *testing.T, w io.Writer, mu *sync.Mutex, v any) {
	t.Helper()

	body, err := json.Marshal(v)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()

	_, err = fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(body))
	require.NoError(t, err)

	_, err = w.Write(body)
	require.NoError(t, err)
}

func writeFrameErr(w io.Writer, mu *sync.Mutex, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}

	mu.Lock()
	defer mu.Unlock()

	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}

	_, err = w.Write(body)

	return err
}

// readFrame reads one framed message, reporting ok=false at EOF.
func readFrame(t *testing.T, r *bufio.Reader) (f rpcFrame, ok bool) {
	t.Helper()

	var length int

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return rpcFrame{}, false
		}

		if line == "\r\n" || line == "\n" {
			break
		}

		if _, err := fmt.Sscanf(line, "Content-Length: %d", &length); err != nil {
			length = 0
		}
	}

	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return rpcFrame{}, false
	}

	require.NoError(t, json.Unmarshal(body, &f))

	return f, true
}

// TestServeStream drives the real stdio transport layer end to end: header
// framing in both directions, request dispatch, async diagnostics
// published to the client, and the shutdown/exit handshake ending
// ServeStream cleanly. ServeStream previously had no direct coverage;
// everything above it was tested through fake servers.
//
// The test runs in a synctest bubble: the diagnostics goroutine is
// asynchronous, and the bubble makes waiting for it deterministic
// (synctest.Wait) instead of wall-clock sleeps. Virtual time keeps the
// timeouts instant in wall time.
func TestServeStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clientConn, serverConn := pipePair()
		defer clientConn.Close()

		ss := NewStreamServer(&Options{})

		errCh := make(chan error, 1)
		go func() {
			errCh <- ss.ServeStream(context.Background(), jsonrpc2.NewConn(jsonrpc2.NewStream(serverConn)))
		}()

		var (
			writeMu sync.Mutex
			frames  = make(chan rpcFrame, 64)
		)

		// The client pump reads everything the server sends, answers any
		// server->client request with an empty result (e.g. capability
		// registration), and forwards frames for assertion.
		go func() {
			defer close(frames)

			r := bufio.NewReader(clientConn)

			for {
				f, ok := readFrame(t, r)
				if !ok {
					return
				}

				if f.isRequest() {
					if err := writeFrameErr(clientConn, &writeMu, map[string]any{
						"jsonrpc": "2.0",
						"id":      f.ID,
						"result":  struct{}{},
					}); err != nil {
						return
					}
				}

				select {
				case frames <- f:
				default: // drop overflow; assertions only need early frames
				}
			}
		}()

		waitFor := func(id int) rpcFrame {
			t.Helper()

			for {
				select {
				case f, ok := <-frames:
					if !ok {
						t.Fatalf("connection closed before response to %d", id)
					}

					if f.isResponse() && f.idInt() == id {
						return f
					}
				case <-time.After(5 * time.Second):
					t.Fatalf("timed out waiting for response to %d", id)
				}
			}
		}

		// initialize -> capabilities come back over the wire.
		writeFrame(t, clientConn, &writeMu, map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "initialize",
			"params":  map[string]any{"processId": nil, "rootUri": nil, "capabilities": map[string]any{}},
		})

		init := waitFor(1)
		assert.Contains(t, string(init.Result), "capabilities")

		// initialized completes the handshake; the server registers
		// capabilities, which the pump answers.
		writeFrame(t, clientConn, &writeMu, map[string]any{
			"jsonrpc": "2.0",
			"method":  "initialized",
			"params":  map[string]any{},
		})

		// didOpen makes the server publish diagnostics for the file in a
		// background goroutine. Wait for the bubble to quiesce: when it
		// returns, the diagnostics goroutine has run to its next blocking
		// point, i.e. the publish has been written to the pipe.
		writeFrame(t, clientConn, &writeMu, map[string]any{
			"jsonrpc": "2.0",
			"method":  "textDocument/didOpen",
			"params": map[string]any{
				"textDocument": map[string]any{
					"uri":        "file://" + t.TempDir() + "/stream.thrift",
					"languageId": "thrift",
					"version":    1,
					"text":       "struct S {\n  1: i32 id\n}\n",
				},
			},
		})

		synctest.Wait()

		assert.True(t, sawMethod(frames, "textDocument/publishDiagnostics"),
			"diagnostics must arrive over the transport")

		// shutdown replies with a null result...
		writeFrame(t, clientConn, &writeMu, map[string]any{
			"jsonrpc": "2.0",
			"id":      2,
			"method":  "shutdown",
		})

		shutdown := waitFor(2)
		assert.Equal(t, "null", string(shutdown.Result))

		// ...and exit plus the client hanging up ends ServeStream: the
		// jsonrpc2 read loop terminates on stream EOF (real clients close
		// stdio), returning whatever transport error the hangup produced.
		writeFrame(t, clientConn, &writeMu, map[string]any{
			"jsonrpc": "2.0",
			"method":  "exit",
		})

		clientConn.Close()

		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
			t.Fatal("ServeStream did not return after exit")
		}

		synctest.Wait()
	})
}

// sawMethod drains buffered frames looking for method.
func sawMethod(frames <-chan rpcFrame, method string) bool {
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				return false
			}

			if f.Method == method {
				return true
			}
		default:
			return false
		}
	}
}
