package source

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/karitham/thrift-ls/lsp/cache"
)

// TestCompletionQualifiedType covers "songs.|" in type positions: the dot
// scopes the completion to the include's types (never enum values or
// consts), the label keeps the include qualifier, and the inserted name is
// the bare type — the edit range starts at the cursor so the dot stays.
func TestCompletionQualifiedType(t *testing.T) {
	songs := `enum Song {
	FUWA_FUWA_TIME = 1
}

struct Album {
	1: required string title
}

const i32 TEMPO = 120`

	tests := []struct {
		name    string
		content string
		marker  string
	}{
		{
			name:    "field type",
			content: "include \"songs.thrift\"\nstruct Club {\n\t1: required songs.\n}",
			marker:  "1: required songs.",
		},
		{
			name:    "const type",
			content: "include \"songs.thrift\"\nconst songs. favorite = Album{title = \"x\"}",
			marker:  "const songs.",
		},
		{
			name:    "function return type",
			content: "include \"songs.thrift\"\nservice Club {\n\tsongs. Play()\n}",
			marker:  "\tsongs.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ss := buildSnapshot(t, nil,
				&cache.FileChange{URI: "file:///tmp/songs.thrift", Version: 0, Content: []byte(songs), From: cache.FileChangeTypeDidOpen},
				&cache.FileChange{URI: "file:///tmp/club.thrift", Version: 0, Content: []byte(tt.content), From: cache.FileChangeTypeDidOpen},
			)

			pos := lspPosOf(t, tt.content, tt.marker)

			labels, rng, _ := completionLabels(t, ss, "file:///tmp/club.thrift", pos)

			assert.Contains(t, labels, "songs.Album", "labels: %v", labels)
			assert.Contains(t, labels, "songs.Song", "labels: %v", labels)
			assert.NotContains(t, labels, "Song.FUWA_FUWA_TIME", "value candidates must not leak into a type slot")
			assert.NotContains(t, labels, "TEMPO", "const candidates must not leak into a type slot")

			// The edit range starts at the cursor: the dot is not replaced.
			assert.Equal(t, pos.Character, rng.Start.Character)
		})
	}
}

// TestCompletionQualifiedTypeFilter pins that typing after the dot filters
// the include's types: "songs.A" narrows to Album.
func TestCompletionQualifiedTypeFilter(t *testing.T) {
	content := "include \"songs.thrift\"\nstruct Club {\n\t1: required songs.A\n}"

	ss := buildSnapshot(t, nil,
		&cache.FileChange{URI: "file:///tmp/songs.thrift", Version: 0, Content: []byte("enum Song {\n\tFUWA_FUWA_TIME = 1\n}\n\nstruct Album {\n\t1: required string title\n}\n"), From: cache.FileChangeTypeDidOpen},
		&cache.FileChange{URI: "file:///tmp/club.thrift", Version: 0, Content: []byte(content), From: cache.FileChangeTypeDidOpen},
	)

	pos := lspPosOf(t, content, "songs.A")

	labels, rng, _ := completionLabels(t, ss, "file:///tmp/club.thrift", pos)

	assert.Equal(t, []string{"songs.Album"}, labels, "labels: %v", labels)

	// The edit replaces the whole typed prefix, keeping the qualifier.
	assert.Equal(t, uint32(13), rng.Start.Character) // 's' of songs.A
	assert.Equal(t, pos.Character, rng.End.Character)
}
