package source

import (
	"strings"
	"testing"

	"go.lsp.dev/protocol"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
)

func TestHover(t *testing.T) {
	file1 := `struct User {
  1: required i64 id
}

enum Color {
  RED = 1,
  GREEN
}

const i32 DEFAULT = 5

service Base { void ping() }`
	file2 := `include "user.thrift"

service Svc extends Base {
  User getUser(1: i64 id, 2: string name) throws (Color c)
}

const Color defaultColor = GREEN`
	view := cache.BuildViewForTest([]*cache.FileChange{
		{URI: "file:///tmp/user.thrift", Version: 0, Content: []byte(file1), From: cache.FileChangeTypeDidOpen},
		{URI: "file:///tmp/api.thrift", Version: 0, Content: []byte(file2), From: cache.FileChangeTypeDidOpen},
	})

	posOf := func(file, marker string, offset ...int) protocol.Position {
		idx := indexOf(file, marker)
		if len(offset) > 0 {
			idx += offset[0]
		}

		if idx < 0 {
			t.Fatalf("marker %q not found", marker)
		}

		line, col := 0, 0

		for i := 0; i < idx; i++ {
			if file[i] == '\n' {
				line++
				col = 0
			} else {
				col++
			}
		}

		return protocol.Position{Line: uint32(line), Character: uint32(col)}
	}

	tests := []struct {
		name    string
		content string
		file    string
		marker  string
		want    string // substring of the hover text
		offset  int
	}{
		{"struct type reference", file2, "file:///tmp/api.thrift", "User getUser", "struct User", 0},
		{"enum type reference", file2, "file:///tmp/api.thrift", "Color c", "enum Color", 0},
		{"service extends", file2, "file:///tmp/api.thrift", "extends Base", "service Base", 8},
		{"service name", file2, "file:///tmp/api.thrift", "service Svc", "service Svc", 8},
		{"const value", file2, "file:///tmp/api.thrift", "= GREEN", "enum Color", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Hover(t.Context(), view, uri.URI(tt.file), posOf(tt.content, tt.marker, tt.offset))
			if err != nil {
				t.Fatalf("Hover: %v", err)
			}

			if !strings.Contains(got, tt.want) {
				t.Errorf("hover text %q does not contain %q", got, tt.want)
			}
		})
	}
}

func TestHoverUnresolvable(t *testing.T) {
	view := cache.BuildViewForTest([]*cache.FileChange{
		{URI: "file:///tmp/test.thrift", Version: 0, Content: []byte("struct S {\n  1: Missing x\n}"), From: cache.FileChangeTypeDidOpen},
	})

	got, err := Hover(t.Context(), view, "file:///tmp/test.thrift", protocol.Position{Line: 1, Character: 8})
	if err != nil {
		t.Fatalf("Hover: %v", err)
	}

	if got != "" {
		t.Errorf("expected empty hover for undefined type, got %q", got)
	}
}
