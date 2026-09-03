package resolver

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// Checker tests file existence by absolute or relative path. It keeps the
// resolver pure string paths: the cache boundary translates uri.URI to OS
// paths before calling Exists.
type Checker interface {
	Exists(ctx context.Context, path string) bool
}

// Resolver resolves include paths for Thrift files.
// It provides a centralized, pure implementation that can be used
// by both CLI and LSP components.
type Resolver struct {
	includePaths []string
	exists       func(ctx context.Context, path string) bool
}

// Option configures a Resolver.
type Option func(*Resolver)

// WithFS checks file existence through fsys. Paths are looked up as given;
// when an absolute path misses, it is retried without the leading slash,
// so os.DirFS("/") and other relative-keyed trees behave like the real
// filesystem. resolvertest.Map and fstest.MapFS make tests hermetic.
func WithFS(fsys fs.FS) Option {
	return WithExistsFunc(func(ctx context.Context, path string) bool {
		if err := ctx.Err(); err != nil {
			return false
		}

		if info, err := fs.Stat(fsys, path); err == nil {
			return !info.IsDir()
		}

		trimmed := strings.TrimPrefix(path, "/")
		if trimmed == path {
			return false
		}

		info, err := fs.Stat(fsys, trimmed)
		if err != nil {
			return false
		}

		return !info.IsDir()
	})
}

// WithChecker checks existence through checker.
func WithChecker(checker Checker) Option {
	return WithExistsFunc(checker.Exists)
}

// WithExistsFunc checks existence through exists.
func WithExistsFunc(exists func(ctx context.Context, path string) bool) Option {
	return func(r *Resolver) {
		r.exists = exists
	}
}

// New creates a Resolver over the real filesystem, unless an Option
// overrides existence checks.
func New(includePaths []string, opts ...Option) *Resolver {
	r := &Resolver{
		includePaths: includePaths,
		exists: func(ctx context.Context, path string) bool {
			if err := ctx.Err(); err != nil {
				return false
			}

			st, err := os.Stat(path)
			if err != nil {
				return false
			}

			return !st.IsDir()
		},
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

// Resolve returns the nearest location of includePath for currentFile: the
// file's directory first, then include paths by proximity (see Candidates).
// When the include resolves nowhere, it returns the file-relative candidate.
func (r *Resolver) Resolve(ctx context.Context, currentFile, includePath string) string {
	if candidates := r.Candidates(ctx, currentFile, includePath); len(candidates) > 0 {
		return candidates[0]
	}

	return filepath.Join(filepath.Dir(currentFile), includePath)
}

// Candidates lists the existing locations of includePath for currentFile,
// nearest first: the file's directory, then include paths ordered by
// shared directory prefix with the file's directory, config order breaking
// ties. More than one location means the include path is shadowed by
// another include path.
func (r *Resolver) Candidates(ctx context.Context, currentFile, includePath string) []string {
	basePath := filepath.Dir(currentFile)

	var candidates []string

	add := func(c string) {
		if ctx.Err() != nil {
			return
		}

		if !r.exists(ctx, c) || containsPath(candidates, c) {
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
