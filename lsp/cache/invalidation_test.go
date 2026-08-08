package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/options"
)

// The gundam test corpus: strike_rouge includes federation.gundam, which
// includes mobile_suit.zeon; char is standalone.
const (
	strikeRouge = "file:///tmp/strike_rouge.thrift"
	federation  = "file:///tmp/federation.gundam.thrift"
	mobileSuit  = "file:///tmp/mobile_suit.zeon.thrift"
	char        = "file:///tmp/char.thrift"
)

func gundamFiles() []*FileChange {
	return []*FileChange{
		{
			URI: uri.URI(strikeRouge),
			Content: []byte(`include "federation.gundam.thrift"

exception BayFull {
	1: string message
}`),
			From: FileChangeTypeDidOpen,
		},
		{
			URI: uri.URI(federation),
			Content: []byte(`include "mobile_suit.zeon.thrift"

struct Gundam {
	1: required string Name
}`),
			From: FileChangeTypeDidOpen,
		},
		{
			URI: uri.URI(mobileSuit),
			Content: []byte(`enum ZeonForces {
	ZAKU_I,
	ZAKU_II,
	GELGOOG
}`),
			From: FileChangeTypeDidOpen,
		},
	}
}

// viewHarness mirrors the server's open-then-change flow: overlays updated
// first, then routed through View.FileChange.
type viewHarness struct {
	view *View
	fs   *overlayFS
}

func newViewHarness(t *testing.T, files []*FileChange) *viewHarness {
	t.Helper()

	c := New()
	fs := NewOverlayFS(c)

	if err := fs.Update(t.Context(), files); err != nil {
		t.Fatal(err)
	}

	view := NewView("file:///tmp", fs, nil, options.Patch{})

	ss, release := view.Snapshot()
	defer release()

	for _, f := range files {
		if _, err := ss.Parse(t.Context(), f.URI); err != nil {
			t.Fatal(err)
		}
	}

	return &viewHarness{view: view, fs: fs}
}

// change applies a change like the server's didChange: overlay first, then
// FileChange, returning the affected URIs passed to the postFn once the
// asynchronous postFn has run.
func (h *viewHarness) change(t *testing.T, change *FileChange) []uri.URI {
	t.Helper()

	if err := h.fs.Update(t.Context(), []*FileChange{change}); err != nil {
		t.Fatal(err)
	}

	done := make(chan []uri.URI, 1)

	h.view.FileChange(t.Context(), []*FileChange{change}, func(a []uri.URI) {
		done <- a
	})

	select {
	case affected := <-done:
		return affected
	case <-time.After(5 * time.Second):
		t.Fatal("postFn did not run")

		return nil
	}
}

func (h *viewHarness) snapshot(t *testing.T) *Snapshot {
	t.Helper()

	ss, release := h.view.Snapshot()
	t.Cleanup(release)

	return ss
}

func Test_FileChangeInvalidatesDependents(t *testing.T) {
	for _, tt := range []struct {
		name         string
		files        []*FileChange
		change       *FileChange
		wantAffected []uri.URI
		wantDropped  []uri.URI
		wantKept     []uri.URI
	}{
		{
			name:  "change mid-chain invalidates transitive dependents",
			files: gundamFiles(),
			change: &FileChange{
				URI:     uri.URI(federation),
				Version: 1,
				Content: []byte(`include "mobile_suit.zeon.thrift"

struct Gundam {
	1: required string Name,
	2: optional i32 SerialNumber
}`),
				From: FileChangeTypeDidChange,
			},
			wantAffected: []uri.URI{federation, strikeRouge},
			wantDropped:  []uri.URI{strikeRouge},
			wantKept:     []uri.URI{federation, mobileSuit},
		},
		{
			name:  "change leaf invalidates whole chain",
			files: gundamFiles(),
			change: &FileChange{
				URI:     uri.URI(mobileSuit),
				Version: 1,
				Content: []byte(`enum ZeonForces {
	ZAKU_I,
	ZAKU_II,
	GELGOOG,
	CHARS_ZAKU
}`),
				From: FileChangeTypeDidChange,
			},
			wantAffected: []uri.URI{mobileSuit, federation, strikeRouge},
			wantDropped:  []uri.URI{federation, strikeRouge},
			wantKept:     []uri.URI{mobileSuit},
		},
		{
			name: "change file with no dependents",
			files: []*FileChange{
				{
					URI: uri.URI(char),
					Content: []byte(`struct Char {
	1: optional string Title
}`),
					From: FileChangeTypeDidOpen,
				},
			},
			change: &FileChange{
				URI:     uri.URI(char),
				Version: 1,
				Content: []byte(`struct Char {
	1: optional string Title,
	2: optional bool Newtype
}`),
				From: FileChangeTypeDidChange,
			},
			wantAffected: []uri.URI{char},
			wantDropped:  nil,
			wantKept:     []uri.URI{char},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newViewHarness(t, tt.files)

			gotAffected := h.change(t, tt.change)
			assert.Equal(t, tt.wantAffected, gotAffected)

			ss := h.snapshot(t)
			for _, file := range tt.wantDropped {
				pf, _ := ss.parsedCache.Get(file)
				assert.Nil(t, pf, "parse cache for %s should be dropped", file)

				_, ok := ss.files.Get(file)
				assert.False(t, ok, "file handle for %s should be dropped", file)
			}

			for _, file := range tt.wantKept {
				pf, _ := ss.parsedCache.Get(file)
				assert.NotNil(t, pf, "parse cache for %s should survive", file)
			}
		})
	}
}
