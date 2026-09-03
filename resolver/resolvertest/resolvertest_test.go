package resolvertest

import (
	"context"
	"io/fs"
	"testing"

	"github.com/karitham/thrift-ls/resolver"
)

// Map stays a valid existence checker for the resolver: if the Checker
// contract drifts, this fails to compile.
var _ resolver.Checker = Map{}

func TestMap(t *testing.T) {
	seed := Map{"/base/shared.thrift": []byte("struct Shared {}")}

	t.Run("exists", func(t *testing.T) {
		tests := []struct {
			name string
			path string
			want bool
		}{
			{name: "seeded file", path: "/base/shared.thrift", want: true},
			{name: "missing file", path: "/base/missing.thrift", want: false},
			{name: "directory prefix is not a file", path: "/base", want: false},
			{name: "relative form misses an absolute seed", path: "base/shared.thrift", want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if got := seed.Exists(t.Context(), tt.path); got != tt.want {
					t.Errorf("Exists(%q) = %v, want %v", tt.path, got, tt.want)
				}
			})
		}
	})

	t.Run("cancelled context finds nothing", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		if seed.Exists(ctx, "/base/shared.thrift") {
			t.Error("Exists with a cancelled context = true, want false")
		}
	})

	t.Run("stat", func(t *testing.T) {
		info, err := fs.Stat(seed, "/base/shared.thrift")
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}

		if info.Name() != "shared.thrift" {
			t.Errorf("Name() = %q, want %q", info.Name(), "shared.thrift")
		}

		if info.IsDir() {
			t.Error("IsDir() = true, want false")
		}

		if _, err := fs.Stat(seed, "/base/missing.thrift"); err == nil {
			t.Error("Stat(missing) = nil, want an error")
		}
	})

	t.Run("open reads seeded content", func(t *testing.T) {
		f, err := seed.Open("/base/shared.thrift")
		if err != nil {
			t.Fatalf("Open: %v", err)
		}

		defer func() {
			if err := f.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		}()

		content := make([]byte, 16)
		n, err := f.Read(content)

		if n != len("struct Shared {}") || string(content) != "struct Shared {}" {
			t.Errorf("Read = %q (n=%d, err=%v), want the seeded content", content, n, err)
		}

		if _, err := seed.Open("/base/missing.thrift"); err == nil {
			t.Error("Open(missing) = nil, want an error")
		}
	})

	t.Run("uris converts keys for FileSource seeds", func(t *testing.T) {
		uris := seed.URIs()

		content, ok := uris["file:///base/shared.thrift"]
		if !ok {
			t.Fatal("URIs() misses the seeded path")
		}

		if string(content) != "struct Shared {}" {
			t.Errorf("URIs() content = %q, want the seeded content", content)
		}
	})

	t.Run("seed builds a presence-only tree", func(t *testing.T) {
		m := Seed("/a.thrift", "/b.thrift")

		for _, p := range []string{"/a.thrift", "/b.thrift"} {
			if !m.Exists(t.Context(), p) {
				t.Errorf("Exists(%q) = false, want true", p)
			}
		}

		if m.Exists(t.Context(), "/c.thrift") {
			t.Error("Exists(/c.thrift) = true, want false")
		}
	})
}
