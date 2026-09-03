package resolver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/karitham/thrift-ls/options"
	"github.com/karitham/thrift-ls/resolver/resolvertest"
)

// TestConfigRelativeIncludePaths verifies that include paths in a config are
// resolved relative to the config file, so they work regardless of the
// process working directory.
func TestConfigRelativeIncludePaths(t *testing.T) {
	dir := t.TempDir()

	cfgPath := filepath.Join(dir, "thrift-ls.json")
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

	// Resolution works from any CWD against an in-memory tree. The
	// config-relative include path is absolute, so the seed keys are too.
	baseFile := filepath.Join(dir, "project", "base", "types.thrift")
	r := New(*cfg.IncludePaths, WithFS(resolvertest.Seed(baseFile)))

	cur := filepath.Join(dir, "project", "app.thrift")
	if got := r.Resolve(t.Context(), cur, "types.thrift"); got != baseFile {
		t.Errorf("Resolve = %q, want %q", got, baseFile)
	}
}

// TestWithFSStripsAbsolutePrefix verifies relative-keyed trees (os.DirFS,
// fstest.MapFS) resolve absolute candidates: the lookup retries without
// the leading slash.
func TestWithFSStripsAbsolutePrefix(t *testing.T) {
	r := New([]string{"/base"}, WithFS(resolvertest.Seed("base/shared.thrift")))

	if got := r.Resolve(t.Context(), "/work/app.thrift", "shared.thrift"); got != "/base/shared.thrift" {
		t.Errorf("Resolve = %q, want %q", got, "/base/shared.thrift")
	}
}

// TestConfigAbsoluteIncludePaths keeps absolute paths as-is.
func TestConfigAbsoluteIncludePaths(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "thrift-ls.json")

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

// TestResolveOrder verifies resolution order against an in-memory tree:
// relative to the current file first, then each include path.
func TestResolveOrder(t *testing.T) {
	r := New([]string{"proj/base", "proj/vendor"}, WithFS(resolvertest.Seed(
		"proj/service/types.thrift",
		"proj/base/types.thrift",
		"proj/base/other.thrift",
		"proj/vendor/types.thrift",
		"proj/vendor/deep/nested.thrift",
	)))
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
			if got := r.Resolve(t.Context(), cur, tt.includePath); got != tt.want {
				t.Errorf("Resolve(%q) = %q, want %q", tt.includePath, got, tt.want)
			}
		})
	}
}
