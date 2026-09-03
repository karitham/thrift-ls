package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
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

// frameBytes serializes v as one Content-Length framed message.
func frameBytes(v any) ([]byte, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer

	fmt.Fprintf(&buf, "Content-Length: %d\r\n\r\n", len(body))
	buf.Write(body)

	return buf.Bytes(), nil
}

// readFrame reads one framed message, reporting ok=false at EOF or on a
// malformed stream.
func readFrame(r *bufio.Reader) (rpcFrame, bool) {
	var length int

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return rpcFrame{}, false
		}

		if line == "\r\n" || line == "\n" {
			break
		}

		_, _ = fmt.Sscanf(line, "Content-Length: %d", &length)
	}

	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return rpcFrame{}, false
	}

	var f rpcFrame
	if err := json.Unmarshal(body, &f); err != nil {
		return rpcFrame{}, false
	}

	return f, true
}

// clientHarness simulates the editor side of the transport. Reads and
// writes run on separate goroutines joined by buffered queues: the reader
// only ever reads, the writer only ever writes. Neither side can deadlock
// against the server by holding one end of the synchronous pipe while
// waiting on the other. (A single read-then-reply loop deadlocked in CI:
// replying to RegisterCapability blocked on the server reading, while the
// server blocked writing it a LogMessage notification that only the busy
// pump would have read.)
type clientHarness struct {
	conn     net.Conn
	frames   chan rpcFrame // every frame the server sends
	outbound chan []byte   // framed messages to deliver
	wg       sync.WaitGroup
}

func newClientHarness(conn net.Conn) *clientHarness {
	h := &clientHarness{
		conn:     conn,
		frames:   make(chan rpcFrame, 256),
		outbound: make(chan []byte, 256),
	}

	h.wg.Add(2)

	go h.readLoop()
	go h.writeLoop()

	return h
}

func (h *clientHarness) readLoop() {
	defer h.wg.Done()
	defer close(h.frames)

	r := bufio.NewReader(h.conn)

	for {
		f, ok := readFrame(r)
		if !ok {
			return
		}

		h.frames <- f

		if f.isRequest() {
			// Auto-answer server->client requests (capability
			// registration) with an empty result.
			b, err := frameBytes(map[string]any{
				"jsonrpc": "2.0",
				"id":      f.ID,
				"result":  struct{}{},
			})
			if err != nil {
				return
			}

			h.outbound <- b
		}
	}
}

func (h *clientHarness) writeLoop() {
	defer h.wg.Done()

	for b := range h.outbound {
		if _, err := h.conn.Write(b); err != nil {
			return
		}
	}
}

// send delivers one message to the server. It never touches the pipe:
// the frame lands in the outbound queue for the writer goroutine.
func (h *clientHarness) send(t *testing.T, v any) {
	t.Helper()

	b, err := frameBytes(v)
	require.NoError(t, err)

	select {
	case h.outbound <- b:
	case <-time.After(30 * time.Second):
		t.Fatal("client outbound queue backed up")
	}
}

// close hangs up the transport and waits for both harness goroutines.
func (h *clientHarness) close() {
	close(h.outbound)
	_ = h.conn.Close()
	h.wg.Wait()
}

const harnessTimeout = 30 * time.Second

// waitFor returns the response with the given id.
func (h *clientHarness) waitFor(t *testing.T, id int) rpcFrame {
	t.Helper()

	for {
		select {
		case f, ok := <-h.frames:
			if !ok {
				t.Fatalf("connection closed before response to %d", id)
			}

			if f.isResponse() && f.idInt() == id {
				return f
			}
		case <-time.After(harnessTimeout):
			t.Fatalf("timed out waiting for response to %d", id)
		}
	}
}

// waitForMethod consumes frames until one with the given method arrives.
// Diagnostics are published asynchronously; blocking on the queue is the
// wait, so no polling or sleeping is involved.
func (h *clientHarness) waitForMethod(t *testing.T, method string) rpcFrame {
	t.Helper()

	for {
		select {
		case f, ok := <-h.frames:
			if !ok {
				t.Fatalf("connection closed before %q arrived", method)
			}

			if f.Method == method {
				return f
			}
		case <-time.After(harnessTimeout):
			t.Fatalf("timed out waiting for %q", method)
		}
	}
}

// TestServeStream drives the real stdio transport layer end to end: header
// framing in both directions, request dispatch, async diagnostics
// published to the client, and the shutdown/exit handshake ending
// ServeStream cleanly.
//
// The test deliberately does not run in a testing/synctest bubble: a
// deadlock inside a bubble freezes virtual time (a hung test instead of a
// fast failure), and jsonrpc2's global waiter pool leaks channels across
// bubbles, making -count>1 a hard runtime error. Every step below is a
// strict send-then-block-on-queue, so wall-clock mode is deterministic
// without sleeping.
func TestServeStream(t *testing.T) {
	clientConn, serverConn := net.Pipe()

	view := NewStreamServer(&Options{})

	errCh := make(chan error, 1)
	go func() {
		errCh <- view.ServeStream(context.Background(), jsonrpc2.NewConn(jsonrpc2.NewStream(serverConn)))
	}()

	client := newClientHarness(clientConn)

	// initialize -> capabilities come back over the wire.
	client.send(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{"processId": nil, "rootUri": nil, "capabilities": map[string]any{}},
	})

	init := client.waitFor(t, 1)
	assert.Contains(t, string(init.Result), "capabilities")

	// initialized completes the handshake; the server registers
	// capabilities, which the harness answers.
	client.send(t, map[string]any{
		"jsonrpc": "2.0",
		"method":  "initialized",
		"params":  map[string]any{},
	})

	// didOpen makes the server publish diagnostics for the file in a
	// background goroutine.
	client.send(t, map[string]any{
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

	diags := client.waitForMethod(t, "textDocument/publishDiagnostics")
	assert.NotEmpty(t, string(diags.Params))

	// shutdown replies with a null result...
	client.send(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "shutdown",
	})

	shutdown := client.waitFor(t, 2)
	assert.Equal(t, "null", string(shutdown.Result))

	// ...and exit plus the client hanging up ends ServeStream: the
	// jsonrpc2 read loop terminates on stream EOF (real clients close
	// stdio), returning whatever transport error the hangup produced.
	client.send(t, map[string]any{
		"jsonrpc": "2.0",
		"method":  "exit",
	})

	client.close()

	select {
	case <-errCh:
	case <-time.After(harnessTimeout):
		t.Fatal("ServeStream did not return after exit")
	}
}

func TestServeStdioTreatsEOFAsCleanShutdown(t *testing.T) {
	var output bytes.Buffer

	err := ServeStdio(t.Context(), &Options{}, bytes.NewReader(nil), &output)
	require.NoError(t, err)
}
