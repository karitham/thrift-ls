package sema

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
)

func Test_IncludeShadowCheck(t *testing.T) {
	tests := []struct {
		name        string
		roots       []string // which sibling roots hold recipes/stew.thrift
		wantCount   int
		wantMessage string // required substring when wantCount > 0
	}{
		{
			name:      "include matching one include path",
			roots:     []string{"senshi"},
			wantCount: 0,
		},
		{
			name:        "include matching two include paths",
			roots:       []string{"senshi", "laios"},
			wantCount:   1,
			wantMessage: "matches multiple include paths",
		},
		{
			name:      "unresolvable include",
			roots:     nil,
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			folder := t.TempDir()

			for _, root := range tt.roots {
				require.NoError(t, os.MkdirAll(filepath.Join(folder, root, "recipes"), 0o755))
				_ = writeThrift(t, folder, filepath.Join(root, "recipes", "stew.thrift"), "struct Monster {}\n")
			}

			content := "include \"recipes/stew.thrift\"\nstruct Party { 1: i32 members }\n"
			require.NoError(t, os.MkdirAll(filepath.Join(folder, "camp"), 0o755))
			filePath := writeThrift(t, folder, filepath.Join("camp", "main.thrift"), content)

			view := buildFolderSnapshotForTestWithPaths(t, folder, []string{
				filepath.Join(folder, "laios"),
				filepath.Join(folder, "senshi"),
			}, []*cache.FileChange{
				{
					URI:     uri.File(filePath),
					Version: 0,
					Content: []byte(content),
					From:    cache.FileChangeTypeDidOpen,
				},
			})

			got := runOne(t, EachFile(&IncludeShadowCheck{}), view, uri.File(filePath))

			require.Len(t, got[uri.File(filePath)], tt.wantCount)

			for _, d := range got[uri.File(filePath)] {
				assert.Equal(t, SeverityWarning, d.Severity)
				assert.Equal(t, CodeIncludeShadow, d.Code)
				assert.Contains(t, d.Message, tt.wantMessage)
			}
		})
	}
}

// buildFolderSnapshotForTestWithPaths is buildFolderSnapshotForTest with
// configured include paths, for include-path shadowing tests.
func buildFolderSnapshotForTestWithPaths(t *testing.T, folder string, includePaths []string, files []*cache.FileChange) *cache.View {
	t.Helper()

	c := cache.NewMemoizedFS()
	fs := cache.NewOverlayFS(c)
	_ = fs.Update(t.Context(), files)

	view := cache.NewView(uri.File(folder), fs, includePaths)

	for _, f := range files {
		_, _ = view.Parse(t.Context(), f.URI)
	}

	return view
}
