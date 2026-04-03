package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// IncludeTestFixture represents a test fixture with include files
type IncludeTestFixture struct {
	RootDir     string   // Root temp directory
	MainDir     string   // Where main files go
	IncludeDirs []string // Include paths
}

// NewIncludeFixture creates a test fixture for include resolution testing
func NewIncludeFixture(tb testing.TB) *IncludeTestFixture {
	tb.Helper()
	root := tb.TempDir()
	return &IncludeTestFixture{
		RootDir: root,
	}
}

// WithIncludePaths creates include directories
func (f *IncludeTestFixture) WithIncludePaths(dirs ...string) *IncludeTestFixture {
	for _, dir := range dirs {
		path := filepath.Join(f.RootDir, dir)
		if err := os.MkdirAll(path, 0755); err != nil {
			panic(err)
		}
		f.IncludeDirs = append(f.IncludeDirs, path)
	}
	return f
}

// WriteFile writes a file relative to the root directory
func (f *IncludeTestFixture) WriteFile(filename, content string) string {
	path := filepath.Join(f.RootDir, filename)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		panic(err)
	}
	return path
}

// WriteInclude writes a file in an include directory
func (f *IncludeTestFixture) WriteInclude(dirIndex int, filename, content string) string {
	if dirIndex >= len(f.IncludeDirs) {
		panic("include directory index out of range")
	}
	path := filepath.Join(f.IncludeDirs[dirIndex], filename)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		panic(err)
	}
	return path
}

// CreateMainFileWithInclude creates a main thrift file that includes another file
func (f *IncludeTestFixture) CreateMainFileWithInclude(mainContent, includePath string) string {
	return f.WriteFile("main.thrift", mainContent)
}
