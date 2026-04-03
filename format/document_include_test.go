package format

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/joyme123/thrift-ls/parser"
)

func TestFormatDocumentWithValidationFull_RelativeInclude(t *testing.T) {
	// Create temp dir structure
	tmpDir := t.TempDir()

	// Create shared.thrift in same directory as main file
	sharedFile := filepath.Join(tmpDir, "shared.thrift")
	sharedContent := `namespace * test

struct User {
    1: string Name
    2: i32 Age
}`
	if err := os.WriteFile(sharedFile, []byte(sharedContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create main.thrift with relative include
	mainFile := filepath.Join(tmpDir, "main.thrift")
	mainContent := `include "shared.thrift"

namespace * test

struct Person {
    1: string Name
}`
	if err := os.WriteFile(mainFile, []byte(mainContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Parse the document
	psr := parser.PEGParser{}
	doc, errs := psr.Parse(mainFile, []byte(mainContent))
	if len(errs) > 0 {
		t.Fatalf("failed to parse: %v", errs)
	}

	// Format with empty include paths (backward compatibility)
	opts := Options{}
	formatted, err := FormatDocumentWithValidationFull(doc, opts, true, []string{}, mainFile)
	if err != nil {
		t.Fatalf("formatting failed: %v", err)
	}

	// Verify the output
	expected := `include "shared.thrift"

namespace * test

struct Person {
    1: string Name
}`
	if formatted != expected {
		t.Errorf("expected:\n%s\n\ngot:\n%s", expected, formatted)
	}

	// Format with include paths (should work the same)
	formatted2, err := FormatDocumentWithValidationFull(doc, opts, true, []string{tmpDir}, mainFile)
	if err != nil {
		t.Fatalf("formatting with include paths failed: %v", err)
	}
	if formatted2 != formatted {
		t.Errorf("formatted output differs with include paths")
	}
}

func TestFormatDocumentWithValidationFull_IncludePath(t *testing.T) {
	// Create temp dir structure with separate include directory
	tmpDir := t.TempDir()
	includeDir := filepath.Join(tmpDir, "includes")
	srcDir := filepath.Join(tmpDir, "src")

	if err := os.MkdirAll(includeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create types.thrift in include directory
	typesFile := filepath.Join(includeDir, "types.thrift")
	typesContent := `namespace * types

enum Status {
    ACTIVE = 1
    INACTIVE = 2
}`
	if err := os.WriteFile(typesFile, []byte(typesContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create main.thrift in src directory
	mainFile := filepath.Join(srcDir, "main.thrift")
	mainContent := `include "types.thrift"

namespace * test

struct Entity {
    1: Status status
}`
	if err := os.WriteFile(mainFile, []byte(mainContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Parse with include paths
	psr := parser.PEGParser{}
	doc, errs := psr.Parse(mainFile, []byte(mainContent))
	if len(errs) > 0 {
		t.Fatalf("failed to parse: %v", errs)
	}

	// Format with include path
	opts := Options{}
	formatted, err := FormatDocumentWithValidationFull(doc, opts, true, []string{includeDir}, mainFile)
	if err != nil {
		t.Fatalf("formatting failed: %v", err)
	}

	// Verify output
	expected := `include "types.thrift"

namespace * test

struct Entity {
    1: Status status
}`
	if formatted != expected {
		t.Errorf("expected:\n%s\n\ngot:\n%s", expected, formatted)
	}
}

func TestFormatDocumentWithValidationFull_NestedIncludes(t *testing.T) {
	// Create nested include structure
	tmpDir := t.TempDir()
	includeDir := filepath.Join(tmpDir, "includes")

	if err := os.MkdirAll(includeDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create common.thrift
	commonFile := filepath.Join(includeDir, "common.thrift")
	commonContent := `namespace * common

struct ID {
    1: string value
}`
	if err := os.WriteFile(commonFile, []byte(commonContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create types.thrift that includes common
	typesFile := filepath.Join(includeDir, "types.thrift")
	typesContent := `include "common.thrift"

namespace * types

struct Entity {
    1: ID id
    2: string name
}`
	if err := os.WriteFile(typesFile, []byte(typesContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create main.thrift that includes types
	mainFile := filepath.Join(tmpDir, "main.thrift")
	mainContent := `include "types.thrift"

namespace * test

struct Container {
    1: Entity entity
}`
	if err := os.WriteFile(mainFile, []byte(mainContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Parse with include paths
	psr := parser.PEGParser{}
	doc, errs := psr.Parse(mainFile, []byte(mainContent))
	if len(errs) > 0 {
		t.Fatalf("failed to parse: %v", errs)
	}

	// Format with include path (nested includes should be resolved)
	opts := Options{}
	formatted, err := FormatDocumentWithValidationFull(doc, opts, true, []string{includeDir}, mainFile)
	if err != nil {
		t.Fatalf("formatting failed: %v", err)
	}

	// Verify basic formatting works
	if formatted == "" {
		t.Error("expected non-empty formatted output")
	}
}

func TestFormatDocumentWithValidationFull_MissingInclude(t *testing.T) {
	// Formatting should still work even if includes are missing
	tmpDir := t.TempDir()

	mainFile := filepath.Join(tmpDir, "main.thrift")
	mainContent := `include "nonexistent.thrift"

namespace * test

struct Data {
    1: string value
}`
	if err := os.WriteFile(mainFile, []byte(mainContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Parse the document
	psr := parser.PEGParser{}
	doc, errs := psr.Parse(mainFile, []byte(mainContent))
	if len(errs) > 0 {
		t.Fatalf("failed to parse: %v", errs)
	}

	// Format without include paths (backward compatibility, no self-val)
	opts := Options{}
	formatted, err := FormatDocumentWithValidationFull(doc, opts, false, []string{}, mainFile)
	if err != nil {
		t.Fatalf("formatting failed: %v", err)
	}

	expected := `include "nonexistent.thrift"

namespace * test

struct Data {
    1: string value
}`
	if formatted != expected {
		t.Errorf("expected:\n%s\n\ngot:\n%s", expected, formatted)
	}
}

func TestFormatDocumentWithValidationFull_BackwardCompat(t *testing.T) {
	// Test backward compatibility: empty include paths with self-validation=false
	tmpDir := t.TempDir()

	mainFile := filepath.Join(tmpDir, "main.thrift")
	mainContent := `namespace * test

struct Point {
    1: i32 X
    2: i32 Y
}`
	if err := os.WriteFile(mainFile, []byte(mainContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Parse
	psr := parser.PEGParser{}
	doc, errs := psr.Parse(mainFile, []byte(mainContent))
	if len(errs) > 0 {
		t.Fatalf("failed to parse: %v", errs)
	}

	// Format without self-validation and no include paths (backward compat path)
	opts := Options{}
	formatted, err := FormatDocumentWithValidationFull(doc, opts, false, []string{}, mainFile)
	if err != nil {
		t.Fatalf("formatting failed: %v", err)
	}

	// Should work fine
	if formatted == "" {
		t.Error("expected non-empty formatted output")
	}

	// Test with self-validation using fallback Parse path
	formatted2, err := FormatDocumentWithValidationFull(doc, opts, true, []string{}, "")
	if err != nil {
		t.Fatalf("formatting with empty currentFile failed: %v", err)
	}
	if formatted2 == "" {
		t.Error("expected non-empty formatted output")
	}
}

func TestFormatDocumentWithValidationFull_MultipleIncludePaths(t *testing.T) {
	// Test with multiple include paths
	tmpDir := t.TempDir()
	includeDir1 := filepath.Join(tmpDir, "includes1")
	includeDir2 := filepath.Join(tmpDir, "includes2")

	if err := os.MkdirAll(includeDir1, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(includeDir2, 0755); err != nil {
		t.Fatal(err)
	}

	// Create file in first include directory
	file1 := filepath.Join(includeDir1, "common.thrift")
	content1 := `namespace * common

struct Base {
    1: string id
}`
	if err := os.WriteFile(file1, []byte(content1), 0644); err != nil {
		t.Fatal(err)
	}

	// Create file in second include directory
	file2 := filepath.Join(includeDir2, "types.thrift")
	content2 := `namespace * types

struct Item {
    1: string name
}`
	if err := os.WriteFile(file2, []byte(content2), 0644); err != nil {
		t.Fatal(err)
	}

	// Create main file that includes both
	mainFile := filepath.Join(tmpDir, "main.thrift")
	mainContent := `include "common.thrift"
include "types.thrift"

namespace * test

struct Container {
    1: Base base
    2: Item item
}`
	if err := os.WriteFile(mainFile, []byte(mainContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Parse
	psr := parser.PEGParser{}
	doc, errs := psr.Parse(mainFile, []byte(mainContent))
	if len(errs) > 0 {
		t.Fatalf("failed to parse: %v", errs)
	}

	// Format with both include paths
	opts := Options{}
	formatted, err := FormatDocumentWithValidationFull(doc, opts, true, []string{includeDir1, includeDir2}, mainFile)
	if err != nil {
		t.Fatalf("formatting failed: %v", err)
	}

	// Verify output
	if formatted == "" {
		t.Error("expected non-empty formatted output")
	}
}
