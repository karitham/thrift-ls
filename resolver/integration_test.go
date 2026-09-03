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
	filename := r.Resolve(t.Context(), mainFile, "middle.thrift")
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
	filename2 := r.Resolve(t.Context(), middleFile, strings.Trim(includePath, "\"'"))
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

		next := r.Resolve(t.Context(), chain[i], includes[0])
		if next != chain[i+1] {
			t.Errorf("expected %q, got %q", chain[i+1], next)
		}
	}
}
