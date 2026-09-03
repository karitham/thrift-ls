package store

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/resolver/resolvertest"
	"github.com/karitham/thrift-ls/syntax"
)

func TestResolver(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "mem")
	baseDir := filepath.Join(root, "base")
	sharedDir := filepath.Join(root, "shared")
	sharedThrift := filepath.Join(sharedDir, "shared.thrift")

	tree := resolvertest.Map{
		sharedThrift: []byte("struct Shared {}"),
	}
	fs := NewOverlayFS(NewMemFS(tree.URIs()))

	view := NewView(uri.File(root), fs, []string{sharedDir})

	resolver := view.Resolver()

	for _, tt := range []struct {
		name string
		fn   func(*testing.T)
	}{
		{
			name: "ResolveInclude/relative_to_current_file",
			fn: func(t *testing.T) {
				currentFile := filepath.Join(baseDir, "current.thrift")

				currentURI := uri.File(currentFile)
				result := resolver.ResolveInclude(t.Context(), currentURI, "local.thrift")

				expected := uri.File(filepath.Join(baseDir, "local.thrift"))
				assert.Equal(t, expected, result)
			},
		},
		{
			name: "ResolveInclude/using_include_paths",
			fn: func(t *testing.T) {
				currentFile := filepath.Join(baseDir, "current.thrift")

				currentURI := uri.File(currentFile)
				result := resolver.ResolveInclude(t.Context(), currentURI, "shared.thrift")

				expected := uri.File(sharedThrift)
				assert.Equal(t, expected, result)
			},
		},
		{
			name: "ResolveInclude/finds_an_open_overlay_missing_from_disk",
			fn: func(t *testing.T) {
				// The include lives only in the editor overlay: an unsaved
				// buffer resolves even with no disk copy.
				overlayURI := uri.File(filepath.Join(sharedDir, "overlay.thrift"))
				assert.NoError(t, fs.Update(t.Context(), []*FileChange{
					{URI: overlayURI, Version: 1, Content: []byte("struct Overlay {}"), From: FileChangeTypeDidOpen},
				}))
				t.Cleanup(func() { fs.Forget(overlayURI) })

				currentURI := uri.File(filepath.Join(baseDir, "current.thrift"))
				assert.Equal(t, overlayURI, resolver.ResolveInclude(t.Context(), currentURI, "overlay.thrift"))
				assert.Contains(t, resolver.ResolveIncludeCandidates(t.Context(), currentURI, "overlay.thrift"), overlayURI)
			},
		},
		{
			name: "ResolveInclude/fallback_uri_when_not_found",
			fn: func(t *testing.T) {
				currentFile := filepath.Join(baseDir, "current.thrift")

				currentURI := uri.File(currentFile)
				result := resolver.ResolveInclude(t.Context(), currentURI, "nonexistent.thrift")

				expected := uri.File(filepath.Join(baseDir, "nonexistent.thrift"))
				assert.Equal(t, expected, result)
			},
		},
		{
			name: "GetIncludePath/matching_name",
			fn: func(t *testing.T) {
				doc := &syntax.Document{
					Nodes: []syntax.Node{
						&syntax.Include{Path: &syntax.Token{Text: "shared.thrift"}},
					},
				}

				result := resolver.GetIncludePath(doc, "shared")
				assert.Equal(t, "shared.thrift", result)
			},
		},
		{
			name: "GetIncludePath/non_matching_name",
			fn: func(t *testing.T) {
				doc := &syntax.Document{
					Nodes: []syntax.Node{
						&syntax.Include{Path: &syntax.Token{Text: "other.thrift"}},
					},
				}

				result := resolver.GetIncludePath(doc, "shared")
				assert.Empty(t, result)
			},
		},
		{
			name: "GetIncludePath/bad_nodes_skipped",
			fn: func(t *testing.T) {
				doc := &syntax.Document{
					Nodes: []syntax.Node{
						&syntax.Include{Path: nil},
						&syntax.Namespace{},
						&syntax.Include{Path: &syntax.Token{Text: "shared.thrift"}},
					},
				}

				result := resolver.GetIncludePath(doc, "shared")
				assert.Equal(t, "shared.thrift", result)
			},
		},
		{
			name: "GetIncludeURI/returns_correct_uri",
			fn: func(t *testing.T) {
				currentFile := filepath.Join(baseDir, "current.thrift")

				currentURI := uri.File(currentFile)
				doc := &syntax.Document{
					Nodes: []syntax.Node{
						&syntax.Include{Path: &syntax.Token{Text: "shared.thrift"}},
					},
				}

				result := resolver.GetIncludeURI(t.Context(), currentURI, doc, "shared")

				expected := uri.File(sharedThrift)
				assert.Equal(t, expected, result)
			},
		},
		{
			name: "GetIncludeURI/not_found_returns_empty",
			fn: func(t *testing.T) {
				currentFile := filepath.Join(baseDir, "current.thrift")

				currentURI := uri.File(currentFile)
				doc := &syntax.Document{
					Nodes: []syntax.Node{
						&syntax.Include{Path: &syntax.Token{Text: "other.thrift"}},
					},
				}

				result := resolver.GetIncludeURI(t.Context(), currentURI, doc, "shared")

				assert.Equal(t, uri.URI(""), result)
			},
		},
	} {
		t.Run(tt.name, tt.fn)
	}
}
