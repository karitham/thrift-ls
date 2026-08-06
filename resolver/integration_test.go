package resolver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/karitham/thrift-ls/syntax"
)

func TestResolver_Integration_NestedIncludes(t *testing.T) {
	// Create temp directory structure
	tmpDir := t.TempDir()
	includeDir := filepath.Join(tmpDir, "includes")

	if err := os.MkdirAll(includeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create base.thrift (no includes)
	baseFile := filepath.Join(includeDir, "base.thrift")
	baseContent := `namespace * base

struct BaseID {
    1: string value
}`
	if err := os.WriteFile(baseFile, []byte(baseContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create middle.thrift (includes base)
	middleFile := filepath.Join(includeDir, "middle.thrift")
	middleContent := `include "base.thrift"

namespace * middle

struct UserID {
    1: BaseID id
    2: string name
}`
	if err := os.WriteFile(middleFile, []byte(middleContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create main.thrift (includes middle, which includes base)
	mainFile := filepath.Join(tmpDir, "main.thrift")
	mainContent := `include "middle.thrift"

namespace * main

struct User {
    1: UserID user
}`
	if err := os.WriteFile(mainFile, []byte(mainContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create resolver with include path
	r := New([]string{includeDir})

	// Resolve middle.thrift from main.thrift's perspective
	filename := r.Resolve(mainFile, "middle.thrift")
	if filename != middleFile {
		t.Errorf("expected %q, got %q", middleFile, filename)
	}
	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("failed to read middle.thrift: %v", err)
	}

	// Parse middle.thrift to find its includes
	middleDoc, errs := syntax.Parse(content)
	for _, e := range errs {
		if e.Severity == syntax.SeverityError {
			t.Fatalf("failed to parse middle.thrift: %v", errs)
		}
	}
	if len(middleDoc.Includes()) == 0 {
		t.Fatal("expected middle.thrift to have includes")
	}
	includePath := middleDoc.Includes()[0].Path.Text
	if strings.Trim(includePath, "\"'") != "base.thrift" {
		t.Errorf("expected include path 'base.thrift', got %q", includePath)
	}

	// Resolve base.thrift from middle.thrift's perspective
	filename2 := r.Resolve(middleFile, strings.Trim(includePath, "\"'"))
	if filename2 != baseFile {
		t.Errorf("expected %q, got %q", baseFile, filename2)
	}

	// Verify content
	content2, err := os.ReadFile(filename2)
	if err != nil {
		t.Fatalf("failed to read base.thrift: %v", err)
	}
	if string(content2) != baseContent {
		t.Errorf("base.thrift content mismatch")
	}
}

func TestResolver_Integration_ResolutionOrder(t *testing.T) {
	// Test that include paths are checked before relative resolution
	tmpDir := t.TempDir()
	includeDir := filepath.Join(tmpDir, "includes")
	srcDir := filepath.Join(tmpDir, "src")

	if err := os.MkdirAll(includeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a version in include directory
	includeVersion := filepath.Join(includeDir, "shared.thrift")
	includeContent := `namespace * shared

struct SharedInInclude {
    1: string value
}`
	if err := os.WriteFile(includeVersion, []byte(includeContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a different version in src directory
	srcVersion := filepath.Join(srcDir, "shared.thrift")
	srcContent := `namespace * shared

struct SharedInSrc {
    1: string name
}`
	if err := os.WriteFile(srcVersion, []byte(srcContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create main file in src
	mainFile := filepath.Join(srcDir, "main.thrift")
	mainContent := `include "shared.thrift"

namespace * test

struct Data {
    1: string field
}`
	if err := os.WriteFile(mainFile, []byte(mainContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create resolver with include path
	r := New([]string{includeDir})

	// Test: IncludeCall should find include path first, not relative
	includeCall := r.IncludeCall(mainFile)
	filename, content, err := includeCall("shared.thrift")
	if err != nil {
		t.Fatalf("failed to resolve shared.thrift: %v", err)
	}

	// Should have found the include directory version
	if filename != includeVersion {
		t.Errorf("expected include path version %q, got %q", includeVersion, filename)
	}
	if string(content) != includeContent {
		t.Error("expected content from include directory")
	}
}

func TestResolver_Integration_RelativeFallback(t *testing.T) {
	// Test that relative resolution works when include paths don't have the file
	tmpDir := t.TempDir()

	// Create file in root (no include directories)
	localFile := filepath.Join(tmpDir, "local.thrift")
	localContent := `namespace * local

struct LocalData {
    1: string value
}`
	if err := os.WriteFile(localFile, []byte(localContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create main file that references local
	mainFile := filepath.Join(tmpDir, "main.thrift")
	mainContent := `include "local.thrift"

namespace * main

struct Container {
    1: LocalData data
}`
	if err := os.WriteFile(mainFile, []byte(mainContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create resolver without include paths
	r := New([]string{})

	// Test: Should fall back to relative resolution
	includeCall := r.IncludeCall(mainFile)
	filename, content, err := includeCall("local.thrift")
	if err != nil {
		t.Fatalf("failed to resolve local.thrift: %v", err)
	}

	if filename != localFile {
		t.Errorf("expected %q, got %q", localFile, filename)
	}
	if string(content) != localContent {
		t.Error("content mismatch")
	}
}

func TestResolver_Integration_MultipleIncludePaths(t *testing.T) {
	// Test resolution order across multiple include paths
	tmpDir := t.TempDir()
	includeDir1 := filepath.Join(tmpDir, "includes1")
	includeDir2 := filepath.Join(tmpDir, "includes2")

	if err := os.MkdirAll(includeDir1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(includeDir2, 0o755); err != nil {
		t.Fatal(err)
	}

	// File only in include path 2
	file2 := filepath.Join(includeDir2, "unique.thrift")
	content2 := `namespace * unique

struct UniqueInDir2 {
    1: string value
}`
	if err := os.WriteFile(file2, []byte(content2), 0o644); err != nil {
		t.Fatal(err)
	}

	// File in both include paths (dir1 should win)
	fileBoth1 := filepath.Join(includeDir1, "both.thrift")
	contentBoth1 := `namespace * both

struct BothFromDir1 {
    1: string value
}`
	if err := os.WriteFile(fileBoth1, []byte(contentBoth1), 0o644); err != nil {
		t.Fatal(err)
	}

	fileBoth2 := filepath.Join(includeDir2, "both.thrift")
	contentBoth2 := `namespace * both

struct BothFromDir2 {
    1: string name
}`
	if err := os.WriteFile(fileBoth2, []byte(contentBoth2), 0o644); err != nil {
		t.Fatal(err)
	}

	mainFile := filepath.Join(tmpDir, "main.thrift")
	mainContent := `include "unique.thrift"
include "both.thrift"`
	if err := os.WriteFile(mainFile, []byte(mainContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create resolver with multiple include paths (dir1 first)
	r := New([]string{includeDir1, includeDir2})
	includeCall := r.IncludeCall(mainFile)

	// Test: unique.thrift should be found in includeDir2
	filename, _, err := includeCall("unique.thrift")
	if err != nil {
		t.Fatalf("failed to resolve unique.thrift: %v", err)
	}
	if filename != file2 {
		t.Errorf("expected %q, got %q", file2, filename)
	}

	// Test: both.thrift should be found in includeDir1 (first in list)
	filename2, _, err := includeCall("both.thrift")
	if err != nil {
		t.Fatalf("failed to resolve both.thrift: %v", err)
	}
	if filename2 != fileBoth1 {
		t.Errorf("expected first include path %q, got %q", fileBoth1, filename2)
	}
}

func TestResolver_Integration_DeeplyNestedIncludes(t *testing.T) {
	// Test deeply nested include chain: a -> b -> c -> d
	tmpDir := t.TempDir()
	includeDir := filepath.Join(tmpDir, "includes")

	if err := os.MkdirAll(includeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// d.thrift (no includes)
	dFile := filepath.Join(includeDir, "d.thrift")
	dContent := `namespace * d

struct D {
    1: string value
}`
	if err := os.WriteFile(dFile, []byte(dContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// c.thrift includes d
	cFile := filepath.Join(includeDir, "c.thrift")
	cContent := `include "d.thrift"

namespace * c

struct C {
    1: D d_field
}`
	if err := os.WriteFile(cFile, []byte(cContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// b.thrift includes c
	bFile := filepath.Join(includeDir, "b.thrift")
	bContent := `include "c.thrift"

namespace * b

struct B {
    1: C c_field
}`
	if err := os.WriteFile(bFile, []byte(bContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// a.thrift includes b
	aFile := filepath.Join(includeDir, "a.thrift")
	aContent := `include "b.thrift"

namespace * a

struct A {
    1: B b_field
}`
	if err := os.WriteFile(aFile, []byte(aContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Walk the include chain: a -> b -> c -> d, resolving each include via
	// the resolver and parsing with the syntax package.
	r := New([]string{includeDir})

	parseIncludes := func(file string) ([]string, error) {
		content, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		doc, errs := syntax.Parse(content)
		for _, e := range errs {
			if e.Severity == syntax.SeverityError {
				return nil, fmt.Errorf("parse %s: %v", file, errs)
			}
		}
		var paths []string
		for _, inc := range doc.Includes() {
			paths = append(paths, strings.Trim(inc.Path.Text, "\"'"))
		}
		return paths, nil
	}

	chain := []string{aFile, bFile, cFile, dFile}
	for i := 0; i < len(chain)-1; i++ {
		includes, err := parseIncludes(chain[i])
		if err != nil {
			t.Fatal(err)
		}
		if len(includes) != 1 {
			t.Fatalf("%s: expected one include, got %v", chain[i], includes)
		}
		next := r.Resolve(chain[i], includes[0])
		if next != chain[i+1] {
			t.Errorf("expected %q, got %q", chain[i+1], next)
		}
	}
}
