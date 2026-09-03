package source

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/store"
	"github.com/karitham/thrift-ls/vfs"
)

// writeTree writes the file contents under dir and returns the directory.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}

	return dir
}

// openTree registers every file of dir with its view, like the server's
// initialization walk does. When only is non-nil, only those file names
// are registered.
func openTree(t *testing.T, session *store.Session, dir string, only map[string]bool) {
	t.Helper()

	folder := uri.File(dir)
	view := session.AddView(folder, nil)

	require.NoError(t, filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".thrift" {
			return nil
		}

		if only != nil && !only[filepath.Base(path)] {
			return nil
		}

		content, err := os.ReadFile(path)
		require.NoError(t, err)

		view.Update(t.Context(), &vfs.FileChange{
			URI:     uri.File(path),
			Version: 0,
			Content: content,
			From:    vfs.FileChangeTypeInitialize,
		})

		return nil
	}))
}

// allWorkspaceSymbols mirrors the server's Symbols handler: one snapshot
// per view (folders ordered by URI), querying each view's known files.
func allWorkspaceSymbols(ctx context.Context, session *store.Session, query string, maxResults int) []protocol.SymbolInformation {
	var res []protocol.SymbolInformation

	views := session.Views()
	sort.Slice(views, func(i, j int) bool { return views[i].Folder() < views[j].Folder() })

	for _, view := range views {
		syms := WorkspaceSymbols(ctx, view, view.KnownFiles(), query, maxResults-len(res))

		res = append(res, syms...)
		if maxResults > 0 && len(res) >= maxResults {
			break
		}
	}

	return res
}

func TestWorkspaceSymbols(t *testing.T) {
	twoFiles := map[string]string{
		"mobile_suit.thrift": `struct MobileSuit {
	1: required string Name,
	2: optional i32 ModelNumber,
}

enum ZeonForces {
	ZAKU_I = 1,
	ZAKU_II,
}`,
		"federation.thrift": `service Federation {
	void Deploy(1: string suitName),
	string Query(),
}

const i32 DEFAULT_HP = 100,
typedef string PilotName`,
	}

	queryTree := map[string]string{
		"a.thrift": `struct MobileSuit { 1: string Name }
struct MobileArmor { 1: string Name }
const i32 ZAKU_HP = 100`,
	}

	capTree := map[string]string{
		"a.thrift": `struct A { 1: string x }
struct B { 1: string x }
struct C { 1: string x }`,
	}

	tests := []struct {
		name   string
		files  map[string]string
		query  string
		max    int
		only   map[string]bool // files to register; nil registers all
		nested bool            // files live in per-folder subdirectories
		want   []string
	}{
		{
			name:  "all symbols across files, members flattened",
			files: twoFiles,
			query: "",
			want: []string{
				"Federation", "Deploy", "Query",
				"DEFAULT_HP", "PilotName",
				"MobileSuit", "Name", "ModelNumber",
				"ZeonForces", "ZAKU_I", "ZAKU_II",
			},
		},
		{
			name:  "empty query matches everything",
			files: queryTree,
			query: "",
			want:  []string{"MobileSuit", "Name", "MobileArmor", "Name", "ZAKU_HP"},
		},
		{
			name:  "query filters case-insensitively",
			files: queryTree,
			query: "MOBILE",
			want:  []string{"MobileSuit", "MobileArmor"},
		},
		{
			name:  "query matches members",
			files: queryTree,
			query: "zaku",
			want:  []string{"ZAKU_HP"},
		},
		{
			name:  "query matching nothing returns empty",
			files: queryTree,
			query: "nothing-matches",
			want:  []string{},
		},
		{
			name:  "cap limits the result",
			files: capTree,
			query: "",
			max:   4,
			want:  []string{"A", "x", "B", "x"},
		},
		{
			name:  "cap counts only matching symbols",
			files: queryTree,
			query: "mobile",
			max:   1,
			want:  []string{"MobileSuit"},
		},
		{
			name:   "multiple workspace folders",
			files:  map[string]string{"a/a.thrift": "struct FromA {}", "b/b.thrift": "struct FromB {}"},
			query:  "",
			nested: true,
			want:   []string{"FromA", "FromB"},
		},
		{
			name:  "unregistered files are excluded",
			files: map[string]string{"known.thrift": "struct Known {}", "unknown.thrift": "struct Unknown {}"},
			query: "",
			only:  map[string]bool{"known.thrift": true},
			want:  []string{"Known"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeTree(t, tt.files)

			session := store.NewSession(vfs.NewMemoizedFS())

			if tt.nested {
				// Each top-level directory is a workspace folder.
				dirs := map[string]bool{}
				for name := range tt.files {
					dirs[filepath.Dir(name)] = true
				}

				for d := range dirs {
					openTree(t, session, filepath.Join(dir, d), nil)
				}
			} else {
				openTree(t, session, dir, tt.only)
			}

			syms := allWorkspaceSymbols(t.Context(), session, tt.query, tt.max)

			names := make([]string, len(syms))
			for i, s := range syms {
				names[i] = s.Name
			}

			assert.Equal(t, tt.want, names)
		})
	}
}

// TestWorkspaceSymbolsKindAndLocation pins the kind and the name range of
// each symbol kind.
func TestWorkspaceSymbolsKindAndLocation(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"shapes.thrift": `struct MobileSuit {
	1: required string Name,
}

enum ZeonForces {
	ZAKU_I = 1,
	ZAKU_II,
}

service Federation {
	void Deploy(1: string suitName),
}

const i32 DEFAULT_HP = 100,
typedef string PilotName`,
	})

	session := store.NewSession(vfs.NewMemoizedFS())
	openTree(t, session, dir, nil)

	file := uri.File(filepath.Join(dir, "shapes.thrift"))

	tests := []struct {
		name string
		kind protocol.SymbolKind
		line uint32
		col  uint32
	}{
		{"MobileSuit", protocol.SymbolKindStruct, 0, 7},
		{"Name", protocol.SymbolKindField, 1, 20},
		{"ZeonForces", protocol.SymbolKindEnum, 4, 5},
		{"ZAKU_II", protocol.SymbolKindEnumMember, 6, 1},
		{"Federation", protocol.SymbolKindInterface, 9, 8},
		{"Deploy", protocol.SymbolKindFunction, 10, 6},
		{"DEFAULT_HP", protocol.SymbolKindConstant, 13, 10},
		{"PilotName", protocol.SymbolKindTypeParameter, 14, 15},
	}

	syms := allWorkspaceSymbols(t.Context(), session, "", 0)

	byName := make(map[string]protocol.SymbolInformation, len(syms))
	for _, s := range syms {
		byName[s.Name] = s
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sym, ok := byName[tt.name]
			require.True(t, ok, "symbol %q missing", tt.name)

			assert.Equal(t, tt.kind, sym.Kind)
			assert.Equal(t, file, sym.Location.URI)
			assert.Equal(t, tt.line, sym.Location.Range.Start.Line)
			assert.Equal(t, tt.col, sym.Location.Range.Start.Character)
		})
	}
}

// TestWorkspaceSymbolsContainerName pins the container name of nested
// symbols: each member carries its enclosing definition's name.
func TestWorkspaceSymbolsContainerName(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"shapes.thrift": `struct MobileSuit {
	1: required string Name,
}

enum ZeonForces {
	ZAKU_I = 1,
}

service Federation {
	void Deploy(1: string suitName),
}`,
	})

	session := store.NewSession(vfs.NewMemoizedFS())
	openTree(t, session, dir, nil)

	tests := []struct {
		name      string
		container *string
	}{
		{"MobileSuit", nil},
		{"Name", new("MobileSuit")},
		{"ZAKU_I", new("ZeonForces")},
		{"Deploy", new("Federation")},
	}

	syms := allWorkspaceSymbols(t.Context(), session, "", 0)

	byName := make(map[string]protocol.SymbolInformation, len(syms))
	for _, s := range syms {
		byName[s.Name] = s
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sym, ok := byName[tt.name]
			require.True(t, ok, "symbol %q missing", tt.name)

			if tt.container == nil {
				assert.Nil(t, sym.ContainerName)

				return
			}

			require.NotNil(t, sym.ContainerName)
			assert.Equal(t, *tt.container, *sym.ContainerName)
		})
	}
}

// TestWorkspaceSymbolsKinds pins the distinct symbol kinds: unions and
// exceptions surface differently from plain structs.
func TestWorkspaceSymbolsKinds(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"shapes.thrift": `struct Gundam {
	1: required string Name,
}

union MobileArmor {
	1: string loadout,
}

exception BayFull {
	1: string message,
}`,
	})

	session := store.NewSession(vfs.NewMemoizedFS())
	openTree(t, session, dir, nil)

	syms := allWorkspaceSymbols(t.Context(), session, "", 0)

	byName := make(map[string]protocol.SymbolInformation, len(syms))
	for _, s := range syms {
		byName[s.Name] = s
	}

	tests := []struct {
		name string
		kind protocol.SymbolKind
	}{
		{"Gundam", protocol.SymbolKindStruct},
		{"MobileArmor", protocol.SymbolKindInterface},
		{"BayFull", protocol.SymbolKindClass},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sym, ok := byName[tt.name]
			require.True(t, ok, "symbol %q missing", tt.name)
			assert.Equal(t, tt.kind, sym.Kind)
		})
	}
}
