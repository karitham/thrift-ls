package resolver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolve_Relative(t *testing.T) {
	// Create temp dir and files
	tmpDir := t.TempDir()

	// Create files
	mainFile := filepath.Join(tmpDir, "main.thrift")
	includeFile := filepath.Join(tmpDir, "types.thrift")

	if err := os.WriteFile(mainFile, []byte("// main"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(includeFile, []byte("// types"), 0644); err != nil {
		t.Fatal(err)
	}

	r := New([]string{})

	// Test resolving relative to main file
	resolved := r.Resolve(mainFile, "types.thrift")
	if resolved != includeFile {
		t.Errorf("expected %q, got %q", includeFile, resolved)
	}
}

func TestResolve_IncludePath(t *testing.T) {
	// Create temp dirs
	tmpDir := t.TempDir()
	includeDir := filepath.Join(tmpDir, "includes")
	mainDir := filepath.Join(tmpDir, "src")

	if err := os.MkdirAll(includeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(mainDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create files
	mainFile := filepath.Join(mainDir, "main.thrift")
	includeFile := filepath.Join(includeDir, "shared.thrift")

	if err := os.WriteFile(mainFile, []byte("// main"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(includeFile, []byte("// shared"), 0644); err != nil {
		t.Fatal(err)
	}

	// Resolver with include path
	r := New([]string{includeDir})

	// Resolve should find file in include path
	resolved := r.Resolve(mainFile, "shared.thrift")
	if resolved != includeFile {
		t.Errorf("expected %q, got %q", includeFile, resolved)
	}
}

func TestResolve_Fallback(t *testing.T) {
	// Create temp dir
	tmpDir := t.TempDir()
	mainFile := filepath.Join(tmpDir, "main.thrift")

	if err := os.WriteFile(mainFile, []byte("// main"), 0644); err != nil {
		t.Fatal(err)
	}

	r := New([]string{})

	// Non-existent file should return relative path as fallback
	resolved := r.Resolve(mainFile, "missing.thrift")
	expected := filepath.Join(tmpDir, "missing.thrift")
	if resolved != expected {
		t.Errorf("expected %q, got %q", expected, resolved)
	}
}

func TestResolveContent(t *testing.T) {
	// Create temp files
	tmpDir := t.TempDir()
	mainFile := filepath.Join(tmpDir, "main.thrift")
	includeFile := filepath.Join(tmpDir, "types.thrift")

	content := []byte("struct User { 1: string Name }")
	if err := os.WriteFile(mainFile, []byte("// main"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(includeFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	r := New([]string{})

	filename, data, err := r.ResolveContent(mainFile, "types.thrift")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filename != includeFile {
		t.Errorf("expected filename %q, got %q", includeFile, filename)
	}
	if string(data) != string(content) {
		t.Errorf("expected content %q, got %q", content, data)
	}
}

func TestResolveContent_NotFound(t *testing.T) {
	// Create temp file but not the include
	tmpDir := t.TempDir()
	mainFile := filepath.Join(tmpDir, "main.thrift")

	if err := os.WriteFile(mainFile, []byte("// main"), 0644); err != nil {
		t.Fatal(err)
	}

	r := New([]string{})

	filename, data, err := r.ResolveContent(mainFile, "missing.thrift")
	if data != nil {
		t.Error("expected nil content for missing file")
	}
	if err == nil {
		t.Error("expected error for missing file")
	}
	// Should still return the attempted path
	expected := filepath.Join(tmpDir, "missing.thrift")
	if filename != expected {
		t.Errorf("expected filename %q, got %q", expected, filename)
	}
}

func TestIncludeCall_IncludePath(t *testing.T) {
	// Create temp dirs with separate include path
	tmpDir := t.TempDir()
	includeDir := filepath.Join(tmpDir, "includes")
	mainDir := filepath.Join(tmpDir, "src")

	if err := os.MkdirAll(includeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(mainDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create files
	mainFile := filepath.Join(mainDir, "main.thrift")
	includeFile := filepath.Join(includeDir, "shared.thrift")

	content := []byte("namespace * test\nstruct User { 1: string Name }")
	if err := os.WriteFile(mainFile, []byte("// main"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(includeFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	r := New([]string{includeDir})
	includeCall := r.IncludeCall(mainFile)

	filename, data, err := includeCall("shared.thrift")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filename != includeFile {
		t.Errorf("expected filename %q, got %q", includeFile, filename)
	}
	if string(data) != string(content) {
		t.Errorf("expected content %q, got %q", content, data)
	}
}

func TestIncludeCall_Fallback(t *testing.T) {
	// Test fallback to relative resolution when not in include paths
	tmpDir := t.TempDir()
	mainFile := filepath.Join(tmpDir, "main.thrift")
	localFile := filepath.Join(tmpDir, "local.thrift")

	if err := os.WriteFile(mainFile, []byte("// main"), 0644); err != nil {
		t.Fatal(err)
	}
	content := []byte("// local file")
	if err := os.WriteFile(localFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	r := New([]string{})
	includeCall := r.IncludeCall(mainFile)

	filename, data, err := includeCall("local.thrift")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filename != localFile {
		t.Errorf("expected filename %q, got %q", localFile, filename)
	}
	if string(data) != string(content) {
		t.Errorf("expected content %q, got %q", content, data)
	}
}

func TestIncludeCall_NilIncludePaths(t *testing.T) {
	// Test with nil include paths
	tmpDir := t.TempDir()
	mainFile := filepath.Join(tmpDir, "main.thrift")
	localFile := filepath.Join(tmpDir, "local.thrift")

	if err := os.WriteFile(mainFile, []byte("// main"), 0644); err != nil {
		t.Fatal(err)
	}
	content := []byte("// local")
	if err := os.WriteFile(localFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	r := New(nil)
	includeCall := r.IncludeCall(mainFile)

	filename, data, err := includeCall("local.thrift")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filename != localFile {
		t.Errorf("expected filename %q, got %q", localFile, filename)
	}
	if string(data) != string(content) {
		t.Errorf("expected content %q, got %q", content, data)
	}
}
