package resolver

import (
	"os"
	"path/filepath"

	"github.com/joyme123/thrift-ls/parser"
)

// Resolver resolves include paths for Thrift files.
// It provides a centralized, pure implementation that can be used
// by both CLI and LSP components.
type Resolver struct {
	includePaths []string
}

// New creates a new Resolver with the given include paths.
func New(includePaths []string) *Resolver {
	return &Resolver{
		includePaths: includePaths,
	}
}

// Resolve resolves an include path relative to the current file.
// It first tries relative to currentFile's directory, then tries each
// configured include path in order. Returns the resolved absolute file path,
// or the relative path as a fallback if not found.
func (r *Resolver) Resolve(currentFile, includePath string) string {
	// First try relative to current file's directory
	basePath := filepath.Dir(currentFile)
	resolvedPath := filepath.Join(basePath, includePath)

	// Check if file exists
	if _, err := os.Stat(resolvedPath); err == nil {
		return resolvedPath
	}

	// Try each configured include path
	for _, ip := range r.includePaths {
		candidatePath := filepath.Join(ip, includePath)
		if _, err := os.Stat(candidatePath); err == nil {
			return candidatePath
		}
	}

	// Return relative path as fallback
	return resolvedPath
}

// ResolveContent resolves and reads the file content for an include path.
// This signature matches parser.IncludeCall and can be used directly
// with parser.PEGParser.ParseRecursively.
func (r *Resolver) ResolveContent(currentFile, includePath string) (filename string, content []byte, err error) {
	filename = r.Resolve(currentFile, includePath)
	content, err = os.ReadFile(filename)
	if err != nil {
		return filename, nil, err
	}
	return filename, content, nil
}

// IncludeCall creates a parser.IncludeCall function for use with ParseRecursively.
// The returned function resolves includes using include paths first, then falls back
// to relative resolution from the initialFile.
func (r *Resolver) IncludeCall(initialFile string) parser.IncludeCall {
	return func(include string) (filename string, content []byte, err error) {
		// Try include paths first
		for _, ip := range r.includePaths {
			candidatePath := filepath.Join(ip, include)
			if _, statErr := os.Stat(candidatePath); statErr == nil {
				content, err = os.ReadFile(candidatePath)
				return candidatePath, content, err
			}
		}

		// Fall back to relative resolution from initial file
		return r.ResolveContent(initialFile, include)
	}
}
