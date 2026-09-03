package sema

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/vfs"
)

func Test_UnusedIncludeCheck(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string // full diagnostic messages
	}{
		{
			name:    "no includes",
			content: "struct S { 1: i32 a }\n",
			want:    nil,
		},
		{
			name:    "unused include",
			content: "include \"shared.thrift\"\nstruct S { 1: i32 a }\n",
			want:    []string{`unused include "shared.thrift"`},
		},
		{
			name:    "used by field type reference",
			content: "include \"shared.thrift\"\nstruct S { 1: shared.User u }\n",
			want:    nil,
		},
		{
			name:    "used by unqualified type reference",
			content: "include \"shared.thrift\"\nstruct S { 1: User u }\n",
			want:    nil,
		},
		{
			name:    "used by nested container type reference",
			content: "include \"shared.thrift\"\nstruct S { 1: list<shared.User> us }\n",
			want:    nil,
		},
		{
			name:    "used by const value identifier",
			content: "include \"shared.thrift\"\nconst i32 X = shared.Color.RED\n",
			want:    nil,
		},
		{
			name:    "used by service extends",
			content: "include \"shared.thrift\"\nservice S extends shared.Base {}\n",
			want:    nil,
		},
		{
			name:    "used by function return and argument types",
			content: "include \"shared.thrift\"\nservice S { shared.User get(1: shared.User u) }\n",
			want:    nil,
		},
		{
			name:    "used by structured annotation type",
			content: "include \"shared.thrift\"\n@shared.User{'id': 1}\nstruct S {}\n",
			want:    nil,
		},
		{
			name:    "used by structured annotation on a field",
			content: "include \"shared.thrift\"\nstruct S { @shared.User(1) 1: i32 a }\n",
			want:    nil,
		},
		{
			name:    "one used, one unused",
			content: "include \"used.thrift\"\ninclude \"unused.thrift\"\nstruct S { 1: used.User u }\n",
			want:    []string{`unused include "unused.thrift"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			folder := t.TempDir()
			_ = writeThrift(t, folder, "shared.thrift", "struct User {\n  1: i32 id,\n}\nenum Color { RED = 1 }\nservice Base {}\n")
			_ = writeThrift(t, folder, "used.thrift", "struct User {\n  1: i32 id,\n}\n")
			_ = writeThrift(t, folder, "unused.thrift", "struct Ghost {}\n")

			filePath := writeThrift(t, folder, "user.thrift", tt.content)

			view := buildFolderSnapshotForTest(t, folder, []*vfs.FileChange{
				{
					URI:     uri.File(filePath),
					Version: 0,
					Content: []byte(tt.content),
					From:    vfs.FileChangeTypeDidOpen,
				},
			})

			got := runOne(t, EachFile(&UnusedIncludeCheck{}), view, uri.File(filePath))[uri.File(filePath)]

			var gotMsgs []string
			for _, d := range got {
				assert.Equal(t, SeverityWarning, d.Severity)
				gotMsgs = append(gotMsgs, d.Message)
			}

			assert.Equal(t, tt.want, gotMsgs)
		})
	}
}
