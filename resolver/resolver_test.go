package resolver

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/karitham/thrift-ls/options"
)

// TestConfigRelativeIncludePaths verifies that include paths in a config are
// resolved relative to the config file, so they work regardless of the
// process working directory.
func TestConfigRelativeIncludePaths(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "thriftls.json")
	if err := os.WriteFile(cfgPath, []byte(`{"includePaths": ["project/base"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := options.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.IncludePaths == nil || len(*cfg.IncludePaths) != 1 {
		t.Fatalf("includePaths = %v", cfg.IncludePaths)
	}
	want := filepath.Join(dir, "project", "base")
	if got := (*cfg.IncludePaths)[0]; got != want {
		t.Errorf("includePaths[0] = %q, want %q", got, want)
	}

	// Resolution works from any CWD, against a hermetic in-memory fs. The
	// config-relative include path is absolute, so the map keys are too
	// (fstest.MapFS only supports relative keys).
	baseFile := filepath.Join(dir, "project", "base", "types.thrift")
	fsys := absMapFS{baseFile: []byte("struct T {}")}
	r := NewWithFS(*cfg.IncludePaths, fsys)
	cur := filepath.Join(dir, "project", "app.thrift")
	if got := r.Resolve(cur, "types.thrift"); got != baseFile {
		t.Errorf("Resolve = %q, want %q", got, baseFile)
	}
}

// absMapFS is an in-memory fs.FS keyed by absolute paths, for hermetic tests
// of absolute include-path resolution.
type absMapFS map[string][]byte

func (m absMapFS) Stat(name string) (fs.FileInfo, error) {
	if _, ok := m[name]; !ok {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
	}
	return absMapFileInfo{name: name}, nil
}

func (m absMapFS) Open(name string) (fs.File, error) {
	data, ok := m[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return &absMapFile{Reader: bytes.NewReader(data), info: absMapFileInfo{name: name}}, nil
}

type absMapFileInfo struct{ name string }

func (absMapFileInfo) Name() string       { return "" }
func (absMapFileInfo) Size() int64        { return 0 }
func (absMapFileInfo) Mode() fs.FileMode  { return 0 }
func (absMapFileInfo) ModTime() time.Time { return time.Time{} }
func (absMapFileInfo) IsDir() bool        { return false }
func (absMapFileInfo) Sys() any           { return nil }

type absMapFile struct {
	*bytes.Reader
	info fs.FileInfo
}

func (f *absMapFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *absMapFile) Close() error               { return nil }

// TestConfigAbsoluteIncludePaths keeps absolute paths as-is.
func TestConfigAbsoluteIncludePaths(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "thriftls.json")
	abs := filepath.Join(dir, "elsewhere")
	if err := os.WriteFile(cfgPath, []byte(`{"includePaths": ["`+abs+`"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := options.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := (*cfg.IncludePaths)[0]; got != abs {
		t.Errorf("includePaths[0] = %q, want %q", got, abs)
	}
}

// TestResolveOrder verifies resolution order against a hermetic fs:
// relative to the current file first, then each include path.
func TestResolveOrder(t *testing.T) {
	fsys := fstest.MapFS{
		"proj/service/types.thrift":      &fstest.MapFile{Data: []byte("local")},
		"proj/base/types.thrift":         &fstest.MapFile{Data: []byte("base")},
		"proj/base/other.thrift":         &fstest.MapFile{Data: []byte("base")},
		"proj/vendor/types.thrift":       &fstest.MapFile{Data: []byte("vendor")},
		"proj/vendor/deep/nested.thrift": &fstest.MapFile{Data: []byte("nested")},
	}
	r := NewWithFS([]string{"proj/base", "proj/vendor"}, fsys)
	cur := "proj/service/order.thrift"

	tests := []struct {
		name        string
		includePath string
		want        string
	}{
		{"local file wins", "types.thrift", "proj/service/types.thrift"},
		{"first include path", "other.thrift", "proj/base/other.thrift"},
		{"second include path", "deep/nested.thrift", "proj/vendor/deep/nested.thrift"},
		{"missing falls back to relative", "missing.thrift", "proj/service/missing.thrift"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.Resolve(cur, tt.includePath); got != tt.want {
				t.Errorf("Resolve(%q) = %q, want %q", tt.includePath, got, tt.want)
			}
		})
	}
}
