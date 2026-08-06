package codejump

import (
	"testing"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/cache"
)

func TestResolveTargetClassification(t *testing.T) {
	src := `include "base.thrift"

struct User {
  1: required i64 id
  2: optional string name = "bob"
  3: i32 d = DEFAULT
}

enum Color { RED = 1, GREEN }

const i32 DEFAULT = 5
const Color defaultColor = GREEN

service Svc extends Base {
  User getUser(1: i64 id, 2: string name) throws (NotFound e)
}
`
	ss := cache.BuildSnapshotForTest([]*cache.FileChange{
		{
			URI:     "file:///tmp/test.thrift",
			Version: 0,
			Content: []byte(src),
			From:    cache.FileChangeTypeDidOpen,
		},
	})

	// Find the position of the first occurrence of a marker text.
	posOf := func(marker string, offset ...int) protocol.Position {
		idx := indexOf(src, marker)
		if len(offset) > 0 {
			idx += offset[0]
		}
		if idx < 0 {
			t.Fatalf("marker %q not found", marker)
		}
		// Convert byte offset to line/character.
		line, col := 0, 0
		for i := 0; i < idx; i++ {
			if src[i] == '\n' {
				line++
				col = 0
			} else {
				col++
			}
		}
		return protocol.Position{Line: uint32(line), Character: uint32(col)}
	}

	tests := []struct {
		name   string
		marker string
		want   TargetKind
		offset int
	}{
		{"type reference", "NotFound", TargetTypeName, 0},
		{"base type has no target", "i64 id", TargetNone, 0},
		{"const value reference", "= DEFAULT", TargetConstValue, 2},
		{"enum value reference", "= GREEN", TargetConstValue, 2},
		{"service name", "Svc", TargetService, 0},
		{"extends", "Base", TargetService, 0},
		{"struct name", "User", TargetDefinition, 0},
		{"field name", "string name", TargetDefinition, 7},
		{"const name", "DEFAULT = 5", TargetDefinition, 0},
		{"enum value declaration name", "GREEN }", TargetDefinition, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, target, err := resolveTarget(t.Context(), ss, "file:///tmp/test.thrift", posOf(tt.marker, tt.offset))
			if err != nil {
				t.Fatalf("resolveTarget: %v", err)
			}
			if target.kind != tt.want {
				t.Errorf("kind = %v, want %v (node %T)", target.kind, tt.want, target.node)
			}
		})
	}
}

func TestResolveTargetNoNode(t *testing.T) {
	ss := cache.BuildSnapshotForTest([]*cache.FileChange{
		{
			URI:     "file:///tmp/test.thrift",
			Version: 0,
			Content: []byte("struct S {\n  1: i32 a\n}\n\nconst i32 X = 1\n"),
			From:    cache.FileChangeTypeDidOpen,
		},
	})
	// Position in the blank line between definitions: resolves to the
	// document itself with no target kind.
	_, target, err := resolveTarget(t.Context(), ss, "file:///tmp/test.thrift", protocol.Position{Line: 3, Character: 0})
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if target.kind != TargetNone {
		t.Errorf("kind = %v, want TargetNone", target.kind)
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
