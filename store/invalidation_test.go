package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/vfs"
)

// The gundam test corpus: strike_rouge includes federation.gundam, which
// includes mobile_suit.zeon; char is standalone.
const (
	strikeRouge = "file:///tmp/strike_rouge.thrift"
	federation  = "file:///tmp/federation.gundam.thrift"
	mobileSuit  = "file:///tmp/mobile_suit.zeon.thrift"
	char        = "file:///tmp/char.thrift"
)

func gundamFiles() []*vfs.FileChange {
	return []*vfs.FileChange{
		{
			URI: uri.URI(strikeRouge),
			Content: []byte(`include "federation.gundam.thrift"

exception BayFull {
	1: string message
}`),
			From: vfs.FileChangeTypeDidOpen,
		},
		{
			URI: uri.URI(federation),
			Content: []byte(`include "mobile_suit.zeon.thrift"

struct Gundam {
	1: required string Name
}`),
			From: vfs.FileChangeTypeDidOpen,
		},
		{
			URI: uri.URI(mobileSuit),
			Content: []byte(`enum ZeonForces {
	ZAKU_I,
	ZAKU_II,
	GELGOOG
}`),
			From: vfs.FileChangeTypeDidOpen,
		},
	}
}

// viewHarness mirrors the server's open-then-change flow: overlays updated
// first, then routed through View.FileChange.
type viewHarness struct {
	view *View
	fs   *vfs.OverlayFS
}

func newViewHarness(t *testing.T, files []*vfs.FileChange) *viewHarness {
	t.Helper()

	c := vfs.NewMemoizedFS()
	fs := vfs.NewOverlayFS(c)

	if err := fs.Update(t.Context(), files); err != nil {
		t.Fatal(err)
	}

	view := NewView("file:///tmp", fs, nil)

	for _, f := range files {
		if _, err := view.Parse(t.Context(), f.URI); err != nil {
			t.Fatal(err)
		}
	}

	return &viewHarness{view: view, fs: fs}
}

// change applies a change like the server's didChange: overlay first, then
// Update, returning the affected URIs.
func (h *viewHarness) change(t *testing.T, change *vfs.FileChange) []uri.URI {
	t.Helper()

	if err := h.fs.Update(t.Context(), []*vfs.FileChange{change}); err != nil {
		t.Fatal(err)
	}

	res := h.view.Update(t.Context(), change)

	return res.Affected
}

func Test_FileChangeInvalidatesDependents(t *testing.T) {
	for _, tt := range []struct {
		name         string
		files        []*vfs.FileChange
		change       *vfs.FileChange
		wantAffected []uri.URI
		wantFresh    map[uri.URI]string // changed files parse to this marker
		wantKept     []uri.URI          // unchanged files keep their original parse
	}{
		{
			name:  "change mid-chain invalidates transitive dependents",
			files: gundamFiles(),
			change: &vfs.FileChange{
				URI:     uri.URI(federation),
				Version: 1,
				Content: []byte(`include "mobile_suit.zeon.thrift"

struct Gundam {
	1: required string Name,
	2: optional i32 SerialNumber
}`),
				From: vfs.FileChangeTypeDidChange,
			},
			wantAffected: []uri.URI{federation, strikeRouge},
			wantFresh:    map[uri.URI]string{federation: "SerialNumber"},
			wantKept:     []uri.URI{mobileSuit},
		},
		{
			name:  "change leaf invalidates whole chain",
			files: gundamFiles(),
			change: &vfs.FileChange{
				URI:     uri.URI(mobileSuit),
				Version: 1,
				Content: []byte(`enum ZeonForces {
	ZAKU_I,
	ZAKU_II,
	GELGOOG,
	CHARS_ZAKU
}`),
				From: vfs.FileChangeTypeDidChange,
			},
			wantAffected: []uri.URI{mobileSuit, federation, strikeRouge},
			wantFresh:    map[uri.URI]string{mobileSuit: "CHARS_ZAKU"},
			wantKept:     nil,
		},
		{
			name: "change file with no dependents",
			files: []*vfs.FileChange{
				{
					URI: uri.URI(char),
					Content: []byte(`struct Char {
	1: optional string Title
}`),
					From: vfs.FileChangeTypeDidOpen,
				},
			},
			change: &vfs.FileChange{
				URI:     uri.URI(char),
				Version: 1,
				Content: []byte(`struct Char {
	1: optional string Title,
	2: optional bool Newtype
}`),
				From: vfs.FileChangeTypeDidChange,
			},
			wantAffected: []uri.URI{char},
			wantFresh:    map[uri.URI]string{char: "Newtype"},
			wantKept:     nil,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newViewHarness(t, tt.files)

			gotAffected := h.change(t, tt.change)
			assert.Equal(t, tt.wantAffected, gotAffected)

			for _, file := range tt.wantKept {
				pf := h.view.parsed(file)
				assert.NotNil(t, pf, "parse of %s should survive", file)
			}

			// Changed content is visible through the view's store.
			for file, marker := range tt.wantFresh {
				pf, err := h.view.Parse(t.Context(), file)
				require.NoError(t, err)

				assert.Contains(t, pf.Tokens(), marker,
					"%s should be parsed from fresh content (want token %q)", file, marker)
			}
		})
	}
}
