package source

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/karitham/thrift-ls/store"
)

// TestCompletionTypeSlotNoLeaks pins that type slots only suggest types:
// no enum values, consts, or identifier-token dumps leak in when the
// parser cannot complete the construct (openers like map< and keywords
// like const/typedef).
func TestCompletionTypeSlotNoLeaks(t *testing.T) {
	inc := `enum Song {
	FUWA_FUWA_TIME = 1
}

const i32 TEMPO = 120

struct Album {
	1: required string title
}`

	tests := []struct {
		name   string
		marker string
	}{
		{name: "after map opener", marker: "1: required map<"},
		{name: "after list opener", marker: "1: required list<"},
		{name: "after set opener", marker: "1: required set<"},
		{name: "after const", marker: "const "},
		{name: "after typedef", marker: "typedef "},
		{name: "map value after comma", marker: "1: required map<i32, "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := "include \"songs.thrift\"\nstruct Club {\n\t" + tt.marker + "\n}"

			view := buildSnapshot(t, nil,
				&store.FileChange{URI: "file:///tmp/songs.thrift", Version: 0, Content: []byte(inc), From: store.FileChangeTypeDidOpen},
				&store.FileChange{URI: "file:///tmp/club.thrift", Version: 0, Content: []byte(content), From: store.FileChangeTypeDidOpen},
			)

			pos := lspPosOf(t, content, tt.marker)

			labels, _, _ := completionLabels(t, view, "file:///tmp/club.thrift", pos)

			assert.NotContains(t, labels, "FUWA_FUWA_TIME", "enum values must not leak into a type slot")
			assert.NotContains(t, labels, "TEMPO", "consts must not leak into a type slot")
			assert.NotContains(t, labels, "Song", "enum names are types but must not appear bare from another file")
		})
	}
}

// TestCompletionImportedTypesQualified pins that types from included files
// are suggested with their include qualifier: a bare Album reference does
// not resolve from club.thrift, so typing "so" must surface songs.Album —
// never a bare Album.
func TestCompletionImportedTypesQualified(t *testing.T) {
	content := "include \"songs.thrift\"\nstruct Club {\n\t1: required so\n}"

	view := buildSnapshot(t, nil,
		&store.FileChange{URI: "file:///tmp/songs.thrift", Version: 0, Content: []byte("struct Album {\n\t1: required string title\n}\n"), From: store.FileChangeTypeDidOpen},
		&store.FileChange{URI: "file:///tmp/club.thrift", Version: 0, Content: []byte(content), From: store.FileChangeTypeDidOpen},
	)

	pos := lspPosOf(t, content, "required so")

	labels, _, _ := completionLabels(t, view, "file:///tmp/club.thrift", pos)

	assert.Equal(t, []string{"songs.Album"}, labels, "labels: %v", labels)
}
