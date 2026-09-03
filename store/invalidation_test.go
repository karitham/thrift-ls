package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/uri"
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
	fs   *OverlayFS
}

func newViewHarness(t *testing.T, files []*FileChange) *viewHarness {
	t.Helper()

	c := NewDiskFS()
	fs := NewOverlayFS(c)

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
func (h *viewHarness) change(t *testing.T, change *FileChange) []uri.URI {
	t.Helper()

	if err := h.fs.Update(t.Context(), []*FileChange{change}); err != nil {
		t.Fatal(err)
	}

	res := h.view.Update(t.Context(), change)

	return res.Affected
}

func Test_FileChangeInvalidatesDependents(t *testing.T) {
	for _, tt := range []struct {
		name         string
		files        []*FileChange
		change       *FileChange
		wantAffected []uri.URI
		wantFresh    map[uri.URI]string // changed files parse to this marker
		wantKept     []uri.URI          // unchanged files keep their original parse
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
			wantFresh:    map[uri.URI]string{federation: "SerialNumber"},
			wantKept:     []uri.URI{mobileSuit},
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
			wantFresh:    map[uri.URI]string{mobileSuit: "CHARS_ZAKU"},
			wantKept:     nil,
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
			wantFresh:    map[uri.URI]string{char: "Newtype"},
			wantKept:     nil,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newViewHarness(t, tt.files)

			kept := make(map[uri.URI]*ParsedFile, len(tt.wantKept))
			for _, file := range tt.wantKept {
				kept[file] = h.view.parsed(file)
				require.NotNil(t, kept[file], "setup: %s must parse before the change", file)
			}

			gotAffected := h.change(t, tt.change)
			assert.Equal(t, tt.wantAffected, gotAffected)

			for _, file := range tt.wantKept {
				pf := h.view.parsed(file)
				require.NotNil(t, pf, "parse of %s should survive", file)
				assert.Same(t, kept[file], pf, "%s was untouched: no re-parse", file)
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

func Test_ViewEvictDropsEdges(t *testing.T) {
	h := newViewHarness(t, gundamFiles())

	gen := h.view.Generation()
	h.view.Evict(uri.URI(federation))

	assert.False(t, h.view.IsCurrent(gen), "eviction bumps the generation")
	assert.Empty(t, h.view.Includers(uri.URI(mobileSuit)), "reverse edge to the evicted file is dropped")
	assert.Empty(t, h.view.Includes(uri.URI(federation)), "evicted file holds no edges")
	assert.False(t, h.view.FileKnown(uri.URI(federation)), "evicted file leaves the view")
	assert.True(t, h.view.FileKnown(uri.URI(strikeRouge)), "dependents stay tracked")
}

func Test_ViewDidCloseForgetsOverlay(t *testing.T) {
	h := newViewHarness(t, gundamFiles())

	closed := &FileChange{URI: uri.URI(federation), Version: 2, From: FileChangeTypeDidClose}
	require.NoError(t, h.fs.Update(t.Context(), []*FileChange{closed}))
	assert.False(t, h.fs.HasOverlay(uri.URI(federation)), "close drops the overlay")

	h.view.Evict(uri.URI(federation))
	assert.False(t, h.view.FileKnown(uri.URI(federation)))
}

func Test_ViewChangeRemovesIncludeEdge(t *testing.T) {
	h := newViewHarness(t, gundamFiles())
	require.Contains(t, h.view.Includes(uri.URI(federation)), uri.URI(mobileSuit))

	h.change(t, &FileChange{
		URI:     uri.URI(federation),
		Version: 1,
		Content: []byte("struct Gundam {\n\t1: required string Name\n}"),
		From:    FileChangeTypeDidChange,
	})

	assert.Empty(t, h.view.Includes(uri.URI(federation)), "deleted include drops its edge")
	assert.Empty(t, h.view.Includers(uri.URI(mobileSuit)), "reverse edge follows")
}
