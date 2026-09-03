package store

import (
	"context"
	"log/slog"
	"slices"
	"strings"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/resolver"
	"github.com/karitham/thrift-ls/syntax"
)

// Resolver provides centralized include path resolution.
type Resolver struct {
	includePaths []string
	central      *resolver.Resolver
}

// newResolver builds a Resolver resolving against src: open files by their
// overlay presence, the rest through cheap existence checks. No content is
// read to test existence.
func newResolver(includePaths []string, src FileSource) *Resolver {
	return &Resolver{
		includePaths: includePaths,
		central:      resolver.New(includePaths, resolver.WithChecker(fileChecker{src: src})),
	}
}

// fileChecker translates the resolver's string paths to URIs at the store boundary
// boundary. Known sources answer cheaply; unknown ones fall back to a read.
type fileChecker struct {
	src FileSource
}

func (c fileChecker) Exists(ctx context.Context, path string) bool {
	if err := ctx.Err(); err != nil {
		return false
	}

	if ex, ok := c.src.(Checker); ok {
		return ex.Exists(ctx, path)
	}

	fh, err := c.src.ReadFile(ctx, uri.File(path))
	if err != nil {
		return false
	}

	_, err = fh.Content()

	return err == nil
}

// IncludePaths returns the include paths configured for this resolver.
func (r *Resolver) IncludePaths() []string {
	return slices.Clone(r.includePaths)
}

// ResolveInclude resolves an include path to a file URI.
// It first tries relative to the current file, then tries each include path.
func (r *Resolver) ResolveInclude(ctx context.Context, cur uri.URI, includePath string) uri.URI {
	// FsPath, not Path: Path keeps the leading slash before a Windows drive
	// letter ("/c:/dir/x.thrift"), which breaks the resolver's filepath ops.
	filePath := cur.FsPath()
	resolvedPath := r.central.Resolve(ctx, filePath, includePath)

	slog.Debug("include resolved", "file", filePath, "include", includePath, "resolved", resolvedPath)

	return uri.File(resolvedPath)
}

// ResolveIncludeCandidates returns the existing locations of includePath
// for cur, nearest first. More than one location means the include path is
// shadowed by another include path.
func (r *Resolver) ResolveIncludeCandidates(ctx context.Context, cur uri.URI, includePath string) []uri.URI {
	filePath := cur.FsPath()

	paths := r.central.Candidates(ctx, filePath, includePath)
	slog.Debug("include candidates", "file", filePath, "include", includePath, "paths", paths)

	uris := make([]uri.URI, len(paths))
	for i, p := range paths {
		uris[i] = uri.File(p)
	}

	return uris
}

// GetIncludePath returns the include path text for a given include name.
// Returns empty string if not found.
func (r *Resolver) GetIncludePath(ast *syntax.Document, includeName string) string {
	for _, include := range ast.Includes() {
		if include.Path == nil {
			continue
		}

		path := include.PathText()

		name := getIncludeNameFromPath(path)
		if name == includeName {
			return path
		}
	}

	return ""
}

// GetIncludeURI returns the URI for an included file by include name.
// Returns empty URI if not found.
func (r *Resolver) GetIncludeURI(ctx context.Context, cur uri.URI, ast *syntax.Document, includeName string) uri.URI {
	path := r.GetIncludePath(ast, includeName)
	if path == "" {
		return ""
	}

	return r.ResolveInclude(ctx, cur, path)
}

// getIncludeNameFromPath extracts the include name from a path like "base.thrift"
func getIncludeNameFromPath(path string) string {
	items := strings.Split(path, "/")
	name := items[len(items)-1]

	return strings.TrimSuffix(name, ".thrift")
}

func BuildViewForTest(files []*FileChange) *View {
	return BuildViewForTestWithPaths(nil, files)
}

// BuildViewForTestWithPaths is BuildViewForTest with configured include
// paths, for cross-project include resolution tests.
func BuildViewForTestWithPaths(includePaths []string, files []*FileChange) *View {
	c := NewDiskFS()
	fs := NewOverlayFS(c)
	_ = fs.Update(context.TODO(), files)

	view := NewView("file:///tmp", fs, includePaths)

	for _, f := range files {
		_, _ = view.Parse(context.TODO(), f.URI)
	}

	return view
}
