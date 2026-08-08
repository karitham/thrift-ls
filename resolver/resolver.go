package resolver

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// absFS adapts an fs.FS so absolute names work: os.DirFS rejects paths
// starting with "/" (fs.ValidPath), but the resolver works with absolute
// paths.
type absFS struct{ fsys fs.FS }

// isWindowsPath reports whether name is a native Windows path (drive letter
// or backslash separators). Such names cannot go through an fs.FS, which
// only accepts slash-separated relative names; the underlying fs is the
// real filesystem anyway (tests use POSIX names), so go straight to the OS.
func isWindowsPath(name string) bool {
	return strings.Contains(name, `\`) ||
		(len(name) >= 2 && name[1] == ':' && (name[0]|0x20) >= 'a' && (name[0]|0x20) <= 'z')
}

func (a absFS) Open(name string) (fs.File, error) {
	if isWindowsPath(name) {
		return os.Open(name)
	}

	return a.fsys.Open(strings.TrimPrefix(name, "/"))
}

func (a absFS) Stat(name string) (fs.FileInfo, error) {
	if isWindowsPath(name) {
		return os.Stat(name)
	}

	return fs.Stat(a.fsys, strings.TrimPrefix(name, "/"))
}

// FS returns an fs.FS over the real filesystem that accepts absolute paths.
func FS() fs.FS { return absFS{os.DirFS("/")} }

// Resolver resolves include paths for Thrift files.
// It provides a centralized, pure implementation that can be used
// by both CLI and LSP components.
type Resolver struct {
	includePaths []string
	fsys         fs.FS
}

// New creates a Resolver over the real filesystem.
func New(includePaths []string) *Resolver {
	return NewWithFS(includePaths, FS())
}

// NewWithFS creates a Resolver that checks file existence through fsys.
// Absolute paths are looked up as-is; os.DirFS("/") reproduces the default
// filesystem behavior, and fstest.MapFS makes tests hermetic.
func NewWithFS(includePaths []string, fsys fs.FS) *Resolver {
	return &Resolver{
		includePaths: includePaths,
		fsys:         fsys,
	}
}

// Resolve resolves an include path relative to the current file.
// It first tries relative to currentFile's directory, then tries each
// configured include path in order. Returns the resolved path, or the
// candidate relative to currentFile's directory as a fallback if not found.
func (r *Resolver) Resolve(currentFile, includePath string) string {
	basePath := filepath.Dir(currentFile)
	resolvedPath := filepath.Join(basePath, includePath)
	if r.exists(resolvedPath) {
		return resolvedPath
	}

	for _, ip := range r.includePaths {
		candidatePath := filepath.Join(ip, includePath)
		if r.exists(candidatePath) {
			return candidatePath
		}
	}

	return resolvedPath
}

// exists reports whether the file exists on the resolver's filesystem.
func (r *Resolver) exists(path string) bool {
	_, err := fs.Stat(r.fsys, path)

	return err == nil
}
