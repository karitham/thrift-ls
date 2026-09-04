package analyzers

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/karitham/thrift-ls/analyzertest"
	"github.com/karitham/thrift-ls/sema"
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
			got := analyzertest.Run(t, sema.EachFile(&UnusedIncludeCheck{}), map[string]string{
				"shared.thrift": "struct User {\n  1: i32 id,\n}\nenum Color { RED = 1 }\nservice Base {}\n",
				"used.thrift":   "struct User {\n  1: i32 id,\n}\n",
				"unused.thrift": "struct Ghost {}\n",
				"user.thrift":   tt.content,
			}, "user.thrift")[analyzertest.URI("user.thrift")]

			var gotMsgs []string
			for _, d := range got {
				assert.Equal(t, sema.SeverityWarning, d.Severity)
				gotMsgs = append(gotMsgs, d.Message)
			}

			assert.Equal(t, tt.want, gotMsgs)
		})
	}
}
