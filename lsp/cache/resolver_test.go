package cache

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/joyme123/thrift-ls/lsp/memoize"
	"github.com/joyme123/thrift-ls/parser"
	"github.com/stretchr/testify/assert"
	"go.lsp.dev/uri"
)

func TestResolver(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "resolver-test")
	assert.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	baseDir := filepath.Join(tmpDir, "base")
	sharedDir := filepath.Join(tmpDir, "shared")
	err = os.MkdirAll(baseDir, 0755)
	assert.NoError(t, err)
	err = os.MkdirAll(sharedDir, 0755)
	assert.NoError(t, err)

	sharedThrift := filepath.Join(sharedDir, "shared.thrift")
	err = os.WriteFile(sharedThrift, []byte(""), 0644)
	assert.NoError(t, err)

	store := &memoize.Store{}
	c := New(store, nil)
	fs := NewOverlayFS(c)

	view := NewView("test", uri.File(tmpDir), fs, store, nil)
	includePaths := []string{sharedDir}
	ss := NewSnapshot(view, store, includePaths)

	resolver := ss.Resolver()

	for _, tt := range []struct {
		name string
		fn   func(*testing.T)
	}{
		{
			name: "ResolveInclude/relative_to_current_file",
			fn: func(t *testing.T) {
				currentFile := filepath.Join(baseDir, "current.thrift")
				err := os.WriteFile(currentFile, []byte(""), 0644)
				assert.NoError(t, err)

				currentURI := uri.File(currentFile)
				result := resolver.ResolveInclude(currentURI, "local.thrift")

				expected := uri.File(filepath.Join(baseDir, "local.thrift"))
				assert.Equal(t, expected, result)
			},
		},
		{
			name: "ResolveInclude/using_include_paths",
			fn: func(t *testing.T) {
				currentFile := filepath.Join(baseDir, "current.thrift")
				err := os.WriteFile(currentFile, []byte(""), 0644)
				assert.NoError(t, err)

				currentURI := uri.File(currentFile)
				result := resolver.ResolveInclude(currentURI, "shared.thrift")

				expected := uri.File(sharedThrift)
				assert.Equal(t, expected, result)
			},
		},
		{
			name: "ResolveInclude/fallback_uri_when_not_found",
			fn: func(t *testing.T) {
				currentFile := filepath.Join(baseDir, "current.thrift")
				err := os.WriteFile(currentFile, []byte(""), 0644)
				assert.NoError(t, err)

				currentURI := uri.File(currentFile)
				result := resolver.ResolveInclude(currentURI, "nonexistent.thrift")

				expected := uri.File(filepath.Join(baseDir, "nonexistent.thrift"))
				assert.Equal(t, expected, result)
			},
		},
		{
			name: "GetIncludePath/matching_name",
			fn: func(t *testing.T) {
				doc := &parser.Document{
					Includes: []*parser.Include{
						{
							Path: &parser.Literal{
								Value: &parser.LiteralValue{Text: "shared.thrift"},
							},
						},
					},
				}

				result := resolver.GetIncludePath(doc, "shared")
				assert.Equal(t, "shared.thrift", result)
			},
		},
		{
			name: "GetIncludePath/non_matching_name",
			fn: func(t *testing.T) {
				doc := &parser.Document{
					Includes: []*parser.Include{
						{
							Path: &parser.Literal{
								Value: &parser.LiteralValue{Text: "other.thrift"},
							},
						},
					},
				}

				result := resolver.GetIncludePath(doc, "shared")
				assert.Equal(t, "", result)
			},
		},
		{
			name: "GetIncludePath/bad_nodes_skipped",
			fn: func(t *testing.T) {
				doc := &parser.Document{
					Includes: []*parser.Include{
						{BadNode: true},
						{
							Path: &parser.Literal{
								Value: &parser.LiteralValue{Text: "shared.thrift"},
							},
						},
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
				err := os.WriteFile(currentFile, []byte(""), 0644)
				assert.NoError(t, err)

				currentURI := uri.File(currentFile)
				doc := &parser.Document{
					Includes: []*parser.Include{
						{
							Path: &parser.Literal{
								Value: &parser.LiteralValue{Text: "shared.thrift"},
							},
						},
					},
				}

				result := resolver.GetIncludeURI(currentURI, doc, "shared")

				expected := uri.File(sharedThrift)
				assert.Equal(t, expected, result)
			},
		},
		{
			name: "GetIncludeURI/not_found_returns_empty",
			fn: func(t *testing.T) {
				currentFile := filepath.Join(baseDir, "current.thrift")
				err := os.WriteFile(currentFile, []byte(""), 0644)
				assert.NoError(t, err)

				currentURI := uri.File(currentFile)
				doc := &parser.Document{
					Includes: []*parser.Include{
						{
							Path: &parser.Literal{
								Value: &parser.LiteralValue{Text: "other.thrift"},
							},
						},
					},
				}

				result := resolver.GetIncludeURI(currentURI, doc, "shared")

				assert.Equal(t, uri.URI(""), result)
			},
		},
	} {
		t.Run(tt.name, tt.fn)
	}
}
