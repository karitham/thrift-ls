package source

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/store"
)

// TestRenameEnumQualifiedValues pins that renaming an enum also updates
// references in value positions (field defaults, const values), where the
// enum name appears as the qualifier of an enum value: songs.Song.FUWA
// becomes songs.NewName.FUWA, keeping the value part.
func TestRenameEnumQualifiedValues(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		cursor  uri.URI
		pos     protocol.Position
		newName string
		// want maps each file to the ordered NewTexts of its edits.
		want map[string][]string
	}{
		{
			name: "cross-file qualified value references",
			files: map[string]string{
				"file:///tmp/songs.thrift": "enum Song {\n  FUWA_FUWA_TIME = 1,\n  MY_SONG = 2\n}\nconst Song favorite = Song.FUWA_FUWA_TIME\n",
				"file:///tmp/club.thrift":  "include \"songs.thrift\"\nstruct Club {\n  1: optional songs.Song favorite = songs.Song.FUWA_FUWA_TIME\n}\n",
			},
			cursor:  "file:///tmp/songs.thrift",
			pos:     protocol.Position{Line: 0, Character: 5}, // 'S' of Song
			newName: "HoukagoTeaTime",
			want: map[string][]string{
				"file:///tmp/songs.thrift": {
					"HoukagoTeaTime", // the const's Song type reference
					"HoukagoTeaTime", // the same-file Song.FUWA_FUWA_TIME qualifier
					"HoukagoTeaTime", // the enum definition under the cursor
				},
				"file:///tmp/club.thrift": {
					"songs.HoukagoTeaTime", // the type reference
					"HoukagoTeaTime",       // the songs.Song.FUWA_FUWA_TIME qualifier
				},
			},
		},
		{
			name: "type rename leaves value references untouched",
			files: map[string]string{
				"file:///tmp/songs.thrift": "enum Song {\n  FUWA_FUWA_TIME = 1\n}\n",
				"file:///tmp/club.thrift":  "include \"songs.thrift\"\nstruct Club {\n  1: optional songs.Song favorite = songs.Song.FUWA_FUWA_TIME\n}\n",
			},
			cursor:  "file:///tmp/club.thrift",
			pos:     protocol.Position{Line: 2, Character: 14}, // 'S' of the songs.Song type reference
			newName: "Track",
			want: map[string][]string{
				"file:///tmp/club.thrift": {
					"songs.Track", // the type reference only
					"Track",       // the identifier under the cursor
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changes := make([]*store.FileChange, 0, len(tt.files))
			for file, src := range tt.files {
				changes = append(changes, &store.FileChange{
					URI:     uri.URI(file),
					Version: 0,
					Content: []byte(src),
					From:    store.FileChangeTypeDidOpen,
				})
			}

			view := store.BuildViewForTest(changes)

			edit, err := Rename(t.Context(), view, tt.cursor, tt.pos, tt.newName)
			require.NoError(t, err)

			for file, wantNewTexts := range tt.want {
				var got []string
				for _, te := range edit.Changes[uri.URI(file)] {
					got = append(got, te.NewText)
				}

				assert.Equal(t, wantNewTexts, got, "edits for %s", file)
			}
		})
	}
}
