package source

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/store"
	"github.com/karitham/thrift-ls/vfs"
)

// TestDefinitionCrossProjectInclude verifies go-to-definition across project
// boundaries: the current file includes "gundam.thrift" which only exists
// under the configured include path (like the compiler's -I). No files are
// written to disk: both files live in the snapshot's overlay, and include
// resolution consults open files before the disk.
func TestDefinitionCrossProjectInclude(t *testing.T) {
	dir := t.TempDir()
	includeDir := filepath.Join(dir, "whitebase")
	appDir := filepath.Join(dir, "zeon", "apis", "v1")

	gundamFile := uri.File(filepath.Join(includeDir, "gundam.thrift"))
	appFile := uri.File(filepath.Join(appDir, "char.thrift"))
	appContent := "include \"gundam.thrift\"\n\nstruct Hangar {\n  1: optional list<gundam.MobileSuit> suits,\n}\n\nstruct Garrison {\n  1: optional MobileSuit ace,\n}\n"

	view := store.BuildViewForTestWithPaths([]string{includeDir}, []*vfs.FileChange{
		{
			URI:     gundamFile,
			Version: 0,
			Content: []byte("namespace * gundam\n\nstruct MobileSuit {\n  1: string model\n}"),
			From:    vfs.FileChangeTypeDidOpen,
		},
		{
			URI:     appFile,
			Version: 0,
			Content: []byte(appContent),
			From:    vfs.FileChangeTypeDidOpen,
		},
	})

	// Marker-based positions: the cursor on "MobileSuit" in both usages.
	posOf := func(marker string, offset ...int) protocol.Position {
		idx := strings.Index(appContent, marker)
		if len(offset) > 0 {
			idx += offset[0]
		}

		lineStart := idx
		for lineStart > 0 && appContent[lineStart-1] != '\n' {
			lineStart--
		}

		line := 0

		for i := 0; i < lineStart; i++ {
			if appContent[i] == '\n' {
				line++
			}
		}

		return protocol.Position{Line: uint32(line), Character: uint32(idx - lineStart)}
	}

	tests := []struct {
		name   string
		marker string
		offset int
	}{
		{
			name:   "qualified cross-project type",
			marker: "gundam.MobileSuit",
			offset: len("gundam."),
		},
		{
			name:   "unqualified cross-project type",
			marker: " 1: optional MobileSuit ace",
			offset: len(" 1: optional "),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			locs, err := Definition(t.Context(), view, appFile, posOf(tt.marker, tt.offset))
			assert.NoError(t, err)
			assert.Len(t, locs, 1, "should find the definition in the included file")
			assert.Equal(t, gundamFile, locs[0].URI)
		})
	}
}
