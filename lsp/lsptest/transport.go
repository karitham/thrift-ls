package lsptest

// transport.go owns the child process side of a session: spawning the
// server binary, bridging its stdio pipes into go.lsp.dev/jsonrpc2 framing,
// draining stderr into a bounded tail, and the shutdown sequence.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

const (
	// maxCapturedStderrBytes bounds how much of the server's stderr tail the
	// harness keeps for failure reports.
	maxCapturedStderrBytes = 8 << 10

	// killAfter bounds how long shutdown waits for a clean exit before
	// killing the process.
	killAfter = 5 * time.Second
)

// rwPipe joins the child's stdout reader and stdin writer into the single
// io.ReadWriteCloser that jsonrpc2.NewStream frames over.
type rwPipe struct {
	readEnd  io.ReadCloser
	writeEnd io.WriteCloser
}

func (p *rwPipe) Read(b []byte) (int, error) { return p.readEnd.Read(b) }

func (p *rwPipe) Write(b []byte) (int, error) { return p.writeEnd.Write(b) }

func (p *rwPipe) Close() error {
	return errors.Join(p.readEnd.Close(), p.writeEnd.Close())
}

// proc owns the spawned process of one session.
type proc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	// tail keeps only the last bytes the child wrote to stderr. os/exec
	// drains stderr through it on its own goroutine, so the child never
	// blocks writing.
	tail *cappedBuffer

	release sync.Once // first stop wins; later stops no-op
}

// newProc spawns command with cwd set to dir and returns it wired for stdio.
func newProc(command []string, dir string) (*proc, error) {
	if len(command) == 0 {
		return nil, fmt.Errorf("lsptest: empty command")
	}

	p := &proc{
		cmd:  exec.Command(command[0], command[1:]...),
		tail: &cappedBuffer{cap: maxCapturedStderrBytes},
	}
	p.cmd.Dir = dir

	stdin, err := p.cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	p.stdin = stdin

	stdout, err := p.cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	p.stdout = stdout

	p.cmd.Stderr = p.tail

	if err := p.cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", command[0], err)
	}

	return p, nil
}

// stdio returns the framed transport endpoint over the child's pipes.
func (p *proc) stdio() io.ReadWriteCloser {
	return &rwPipe{readEnd: p.stdout, writeEnd: p.stdin}
}

// stop tears the process down: stdin closes first since that is the
// server's normal exit condition; a watchdog kills it if it misses
// killAfter. Wait reports the process failure itself; teardown already
// knows the session ended. Safe to call more than once.
func (p *proc) stop() {
	p.release.Do(func() {
		_ = p.stdin.Close()

		watchdog, cancelWatchdog := context.WithTimeout(context.Background(), killAfter)
		defer cancelWatchdog()

		killer := context.AfterFunc(watchdog, func() { _ = p.cmd.Process.Kill() })
		defer killer()

		_ = p.cmd.Wait()
	})
}

// cappedBuffer keeps only the last cap bytes written to it; anything older
// is dropped.
type cappedBuffer struct {
	mu  sync.Mutex
	cap int
	buf []byte
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.buf = append(b.buf, p...)
	if len(b.buf) > b.cap {
		b.buf = append([]byte(nil), b.buf[len(b.buf)-b.cap:]...)
	}

	return len(p), nil
}

func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return string(b.buf)
}
