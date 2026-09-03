package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/syntax"
	"github.com/karitham/thrift-ls/vfs"
)

// gundam-themed URIs, fixed for deterministic sort order across tests:
// char < federation.gundam < mobile_suit.zeon < strike_rouge.
const (
	charURI        = "file:///tmp/char.thrift"
	federationURI  = "file:///tmp/federation.gundam.thrift"
	mobileSuitURI  = "file:///tmp/mobile_suit.zeon.thrift"
	strikeRougeURI = "file:///tmp/strike_rouge.thrift"
)

// resolveTestInclude resolves an include path relative to the including
// file's directory, mirroring the snapshot resolver's relative fallback.
func resolveTestInclude(_ context.Context, cur uri.URI, includePath string) uri.URI {
	return uri.File(filepath.Join(filepath.Dir(cur.Path()), includePath))
}

// seedEdges records file's include edges on the view without parsing real
// content: the map value is the list of files the key file includes.
func seedEdges(t *testing.T, v *View, edges map[string][]string) {
	t.Helper()

	for file, includes := range edges {
		inc := make([]*syntax.Include, 0, len(includes))
		for _, includePath := range includes {
			inc = append(inc, &syntax.Include{Path: &syntax.Token{Text: includePath}})
		}

		v.setEntry(uri.URI(file), &viewEntry{}, resolveIncludes(t.Context(), uri.URI(file), inc, resolveTestInclude))
	}
}

func Test_View_Dependents(t *testing.T) {
	for _, tt := range []struct {
		name  string
		edges map[string][]string
		file  string
		want  []uri.URI
	}{
		{
			name: "linear chain",
			edges: map[string][]string{
				strikeRougeURI: {"federation.gundam.thrift"},
				federationURI:  {"mobile_suit.zeon.thrift"},
			},
			file: mobileSuitURI,
			want: []uri.URI{federationURI, strikeRougeURI},
		},
		{
			name: "diamond",
			edges: map[string][]string{
				strikeRougeURI: {"federation.gundam.thrift", "mobile_suit.zeon.thrift"},
				federationURI:  {"char.thrift"},
				mobileSuitURI:  {"char.thrift"},
			},
			file: charURI,
			want: []uri.URI{federationURI, mobileSuitURI, strikeRougeURI},
		},
		{
			name: "three cycle terminates",
			edges: map[string][]string{
				strikeRougeURI: {"federation.gundam.thrift"},
				federationURI:  {"mobile_suit.zeon.thrift"},
				mobileSuitURI:  {"strike_rouge.thrift"},
			},
			file: strikeRougeURI,
			want: []uri.URI{federationURI, mobileSuitURI, strikeRougeURI},
		},
		{
			name: "self include terminates",
			edges: map[string][]string{
				"file:///tmp/side_effect.thrift": {"side_effect.thrift"},
			},
			file: "file:///tmp/side_effect.thrift",
			want: []uri.URI{"file:///tmp/side_effect.thrift"},
		},
		{
			name: "file with no includers",
			edges: map[string][]string{
				strikeRougeURI: {"federation.gundam.thrift"},
			},
			file: strikeRougeURI,
			want: []uri.URI{},
		},
		{
			name:  "unknown file",
			edges: map[string][]string{},
			file:  charURI,
			want:  []uri.URI{},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v := NewView("file:///tmp", nil, nil)
			seedEdges(t, v, tt.edges)

			got := v.Dependents(uri.URI(tt.file))
			assert.Equal(t, tt.want, got)
		})
	}
}

// Test_View_ReparseReplacesEdges pins that setEntry drops stale reverse
// edges: after a re-parse removes an include, the former dependency keeps
// only its remaining dependents.
func Test_View_ReparseReplacesEdges(t *testing.T) {
	v := NewView("file:///tmp", nil, nil)

	seedEdges(t, v, map[string][]string{
		strikeRougeURI: {"federation.gundam.thrift", "mobile_suit.zeon.thrift"},
	})

	assert.Equal(t, []uri.URI{strikeRougeURI}, v.Dependents(uri.URI(mobileSuitURI)))

	// Re-parse without the second include.
	seedEdges(t, v, map[string][]string{
		strikeRougeURI: {"federation.gundam.thrift"},
	})

	assert.Empty(t, v.Dependents(uri.URI(mobileSuitURI)))
	assert.Equal(t, []uri.URI{strikeRougeURI}, v.Dependents(uri.URI(federationURI)))
	assert.Empty(t, v.Includers(uri.URI(mobileSuitURI)))
}

func Test_View_IncludesAndIncluders(t *testing.T) {
	v := NewView("file:///tmp", nil, nil)

	seedEdges(t, v, map[string][]string{
		strikeRougeURI: {"char.thrift", "federation.gundam.thrift"},
	})

	assert.Equal(t,
		[]uri.URI{uri.URI(charURI), uri.URI(federationURI)},
		v.Includes(uri.URI(strikeRougeURI)),
	)
	assert.Equal(t,
		[]uri.URI{uri.URI(strikeRougeURI)},
		v.Includers(uri.URI(federationURI)),
	)
	assert.Empty(t, v.Includes(uri.URI(charURI)))

	// Duplicate include statements collapse to one edge.
	seedEdges(t, v, map[string][]string{
		federationURI: {"federation.gundam.thrift", "federation.gundam.thrift"},
	})
	assert.Equal(t, []uri.URI{uri.URI(federationURI)}, v.Includes(uri.URI(federationURI)))
}

func Test_View_GenerationAndIsCurrent(t *testing.T) {
	h := newViewHarness(t, gundamFiles())

	before := h.view.Generation()
	assert.True(t, h.view.IsCurrent(before))

	h.change(t, &vfs.FileChange{
		URI:     uri.URI(federation),
		Version: 1,
		Content: []byte(`include "mobile_suit.zeon.thrift"`),
		From:    vfs.FileChangeTypeDidChange,
	})

	assert.Greater(t, h.view.Generation(), before, "vfs.FileChange bumps the generation")
	assert.False(t, h.view.IsCurrent(before), "a generation older than the latest is not current")
	assert.True(t, h.view.IsCurrent(h.view.Generation()))
}

// Test_ViewParseIncludeCycles exercises include cycles through the full
// parse path: parsing registers edges, and the graph must settle without
// infinite recursion.
func Test_ViewParseIncludeCycles(t *testing.T) {
	dir := t.TempDir()
	char := uri.File(filepath.Join(dir, "char.thrift"))
	amuro := uri.File(filepath.Join(dir, "amuro.thrift"))
	self := uri.File(filepath.Join(dir, "side_effect.thrift"))

	files := []*vfs.FileChange{
		{URI: char, Content: []byte(`include "amuro.thrift"`), From: vfs.FileChangeTypeDidOpen},
		{URI: amuro, Content: []byte(`include "char.thrift"`), From: vfs.FileChangeTypeDidOpen},
		{URI: self, Content: []byte(`include "side_effect.thrift"`), From: vfs.FileChangeTypeDidOpen},
	}

	ss := BuildViewForTest(files)

	// both directions of the mutual cycle are recorded
	assert.Equal(t, []uri.URI{amuro}, ss.Includes(char))
	assert.Equal(t, []uri.URI{amuro}, ss.Includers(char))

	// dependents terminate on the cycle and include both files
	assert.Equal(t, []uri.URI{amuro, char}, ss.Dependents(char))
	assert.Equal(t, []uri.URI{amuro, char}, ss.Dependents(amuro))

	// self-include: the file is its own dependent, and settles
	assert.Equal(t, []uri.URI{self}, ss.Dependents(self))
}
