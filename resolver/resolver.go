package resolver

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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

// Resolve returns the nearest location of includePath for currentFile: the
// file's directory first, then include paths by proximity (see Candidates).
// When the include resolves nowhere, it returns the file-relative candidate.
func (r *Resolver) Resolve(currentFile, includePath string) string {
	if candidates := r.Candidates(currentFile, includePath); len(candidates) > 0 {
		return candidates[0]
	}

	return filepath.Join(filepath.Dir(currentFile), includePath)
}

// Candidates lists the existing locations of includePath for currentFile,
// nearest first: the file's directory, then include paths ordered by
// shared directory prefix with the file's directory, config order breaking
// ties. More than one location means the include path is shadowed by
// another include path.
func (r *Resolver) Candidates(currentFile, includePath string) []string {
	basePath := filepath.Dir(currentFile)

	var candidates []string

	add := func(c string) {
		if !r.exists(c) || containsPath(candidates, c) {
			return
		}

		candidates = append(candidates, c)
	}

	add(filepath.Join(basePath, includePath))

	for _, ip := range sortByProximity(r.includePaths, basePath) {
		add(filepath.Join(ip, includePath))
	}

	return candidates
}

// sortByProximity orders paths by descending shared directory prefix with
// dir, ties keeping input order. It does not modify paths.
func sortByProximity(paths []string, dir string) []string {
	sorted := slices.Clone(paths)

	slices.SortStableFunc(sorted, func(a, b string) int {
		return sharedDirPrefix(b, dir) - sharedDirPrefix(a, dir)
	})

	return sorted
}

// sharedDirPrefix counts the components a and b share. On Windows builds
// comparison ignores case: fs paths lowercase the drive letter there,
// while config-anchored paths keep the written casing. On other builds
// comparison is case-sensitive: case-differing prefixes are different
// directories.
func sharedDirPrefix(a, b string) int {
	windows := runtime.GOOS == "windows"

	return sharedPrefixLen(sharedComponents(a, windows), sharedComponents(b, windows), windows)
}

// sharedPrefixLen counts the components as and bs share.
func sharedPrefixLen(as, bs []string, windows bool) int {
	n := 0
	for n < len(as) && n < len(bs) && sameComponent(as[n], bs[n], windows) {
		n++
	}

	return n
}

// sharedComponents splits a path into components. On Windows builds
// backslashes convert to slashes first: filepath.Clean does not treat them
// as separators on other builds.
func sharedComponents(path string, windows bool) []string {
	if windows {
		path = strings.ReplaceAll(path, `\`, `/`)
	}

	return strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
}

// sameComponent compares one component, ignoring case when windows is set.
func sameComponent(a, b string, windows bool) bool {
	if windows {
		return strings.EqualFold(a, b)
	}

	return a == b
}

// samePath reports whether two candidate paths name the same file. On
// Windows builds comparison ignores case and separator style, because fs
// paths lowercase the drive letter while config-anchored paths keep the
// written casing.
func samePath(a, b string) bool {
	if runtime.GOOS != "windows" {
		return a == b
	}

	norm := func(p string) string {
		return strings.ReplaceAll(p, `\`, `/`)
	}

	return strings.EqualFold(norm(a), norm(b))
}

// containsPath reports whether candidates already hold p, matching Windows
// paths case-insensitively (see samePath).
func containsPath(candidates []string, p string) bool {
	for _, c := range candidates {
		if samePath(c, p) {
			return true
		}
	}

	return false
}

// exists reports whether the file exists on the resolver's filesystem.
func (r *Resolver) exists(path string) bool {
	_, err := fs.Stat(r.fsys, path)

	return err == nil
}
