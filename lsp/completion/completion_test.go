package completion

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/memoize"
	"github.com/karitham/thrift-ls/lsp/types"
)

// buildSnapshot builds a snapshot from file contents with optional include
// paths.
func buildSnapshot(t *testing.T, includePaths []string, files ...*cache.FileChange) *cache.Snapshot {
	t.Helper()
	store := &memoize.Store{}
	c := cache.New(store, nil)
	fs := cache.NewOverlayFS(c)
	_ = fs.Update(t.Context(), files)
	view := cache.NewView("test", uri.File("/tmp"), fs, store, includePaths)
	return cache.NewSnapshot(view, store, includePaths)
}

func TestSemanticCompletion(t *testing.T) {
	userFile := `struct User {
  1: required i64 id
}

enum Color {
  RED = 1,
  GREEN
}

const i32 DEFAULT = 5`

	apiFile := `include "user.thrift"

struct Profile {
  1: required User user
  2: optional string bio
}

const i32 LIMIT = 10`

	ss := buildSnapshot(t, nil,
		&cache.FileChange{URI: "file:///tmp/user.thrift", Version: 0, Content: []byte(userFile), From: cache.FileChangeTypeDidOpen},
		&cache.FileChange{URI: "file:///tmp/api.thrift", Version: 0, Content: []byte(apiFile), From: cache.FileChangeTypeDidOpen},
	)

	tests := []struct {
		name    string
		file    string
		pos     types.Position
		want    []string
		notWant []string
	}{
		{
			name: "type position completes type names from includes",
			file: "file:///tmp/api.thrift",
			pos:  types.Position{Line: 3, Character: 15}, // on "User" in "1: required User user"
			want: []string{"User", "Profile", "Color"},
		},
		{
			name:    "value position completes consts and enum values",
			file:    "file:///tmp/api.thrift",
			pos:     types.Position{Line: 7, Character: 19}, // on "10" in "const i32 LIMIT = 10"
			want:    []string{"DEFAULT", "RED", "Color.GREEN", "LIMIT"},
			notWant: []string{"User", "Profile"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pf, err := ss.Parse(t.Context(), uri.URI(tt.file))
			assert.NoError(t, err)

			pos, err := pf.Mapper().LSPPosToParserPosition(tt.pos)
			assert.NoError(t, err)

			cands := semanticCandidates(t.Context(), ss, uri.URI(tt.file), pf, pos)
			got := make(map[string]bool)
			for _, c := range cands {
				got[c.showText] = true
			}
			for _, w := range tt.want {
				assert.True(t, got[w], "missing candidate %q in %v", w, got)
			}
			for _, nw := range tt.notWant {
				assert.False(t, got[nw], "unexpected candidate %q in %v", nw, got)
			}
		})
	}
}

func TestCompletionEndToEnd(t *testing.T) {
	content := "struct User {\n  1: required i64 id\n}\n\nstruct Profile {\n  1: required Us\n}"
	ss := buildSnapshot(t, nil,
		&cache.FileChange{URI: "file:///tmp/test.thrift", Version: 0, Content: []byte(content), From: cache.FileChangeTypeDidOpen},
	)

	fh, err := ss.ReadFile(t.Context(), "file:///tmp/test.thrift")
	assert.NoError(t, err)

	cmp := &CompletionRequest{
		Fh:  fh,
		Pos: types.Position{Line: 5, Character: 16}, // after "Us" in "1: required Us"
	}
	items, _, err := DefaultTokenCompletion.Completion(t.Context(), ss, cmp)
	assert.NoError(t, err)

	labels := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, item.Label)
	}
	// The type position completes the struct name from the same file.
	assert.Contains(t, labels, "User")
}
