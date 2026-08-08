package cache

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	type args struct {
		fh FileHandle
	}

	tests := []struct {
		name      string
		args      args
		assertion assert.ErrorAssertionFunc
	}{
		{
			name: "normal",
			args: args{
				fh: &Overlay{
					uri: "file:///tmp/types.thrift",
					content: []byte(`
#include "base.thrift"
struct Xtruct3
{
  1:  string string_thing,
  4:  i32    changed,
  9:  i32    i32_thing,
  11: i64    i64_thing
}
					`),
					version: 0,
				},
			},
			assertion: assert.NoError,
		},
		{
			name: "invalid ast",
			args: args{
				fh: &Overlay{
					uri: "file:///tmp/types.thrift",
					content: []byte(`
#include "base.thrift"
struct Xtruct3
{
  1:  string string_thing,
  4:  i32    changed,
  9:  i32    i32_thing,
  11: i64    i64_thing,
  12: 
}
					`),
					version: 0,
				},
			},
			assertion: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.args.fh)
			tt.assertion(t, err)
			t.Logf("got: %v\n", got)
		})
	}
}

// TestParsedFileDefinitions pins the definition and enum-value indexes:
// every top-level definition is reachable by name, enum values by name.
func TestParsedFileDefinitions(t *testing.T) {
	ss := BuildSnapshotForTest([]*FileChange{
		{URI: "file:///tmp/test.thrift", Version: 0, Content: []byte(`struct S {
	1: required string Name,
}

union U {
	1: string x,
}

exception X {
	1: string m,
}

enum Color {
	RED,
	GREEN,
}

service Fed {
	void go(),
}

const i32 LIMIT = 1,
typedef string PilotName`), From: FileChangeTypeDidOpen},
	})

	pf, err := ss.Parse(t.Context(), "file:///tmp/test.thrift")
	require.NoError(t, err)

	defs := pf.Definitions()
	require.Len(t, defs, 7)

	for _, name := range []string{"S", "U", "X", "Color", "Fed", "LIMIT", "PilotName"} {
		_, ok := defs[name]
		assert.True(t, ok, "definition %q missing", name)
	}

	values := pf.EnumValues()
	require.Len(t, values, 2)
	assert.NotNil(t, values["RED"])
	assert.NotNil(t, values["GREEN"])
}
