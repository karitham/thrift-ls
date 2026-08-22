package cache

import (
	"bytes"
	"context"
	"io/fs"
	"strings"
	"time"

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
// overlay content, the rest through the memoized disk source.
func newResolver(includePaths []string, src FileSource) *Resolver {
	return &Resolver{
		includePaths: includePaths,
		central:      resolver.NewWithFS(includePaths, viewFS{fs: src}),
	}
}

// IncludePaths returns the include paths configured for this resolver.
func (r *Resolver) IncludePaths() []string {
	return r.includePaths
}

// ResolveInclude resolves an include path to a file URI.
// It first tries relative to the current file, then tries each include path.
func (r *Resolver) ResolveInclude(cur uri.URI, includePath string) uri.URI {
	// FsPath, not Path: Path keeps the leading slash before a Windows drive
	// letter ("/c:/dir/x.thrift"), which breaks the resolver's filepath ops.
	filePath := cur.FsPath()
	resolvedPath := r.central.Resolve(filePath, includePath)

	return uri.File(resolvedPath)
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
func (r *Resolver) GetIncludeURI(cur uri.URI, ast *syntax.Document, includeName string) uri.URI {
	path := r.GetIncludePath(ast, includeName)
	if path == "" {
		return ""
	}

	return r.ResolveInclude(cur, path)
}

// getIncludeNameFromPath extracts the include name from a path like "base.thrift"
func getIncludeNameFromPath(path string) string {
	items := strings.Split(path, "/")
	name := items[len(items)-1]

	return strings.TrimSuffix(name, ".thrift")
}

// viewFS adapts a FileSource to fs.FS for include resolution: files open in
// the editor resolve by their overlay content, everything else falls
// through to the memoized disk source. This lets includes resolve for files
// that are open but not yet saved.
type viewFS struct {
	fs FileSource
}

func (f viewFS) Stat(name string) (fs.FileInfo, error) {
	content, err := readThrough(name, f.fs)
	if err != nil {
		return nil, err
	}

	return viewFileInfo{name: name, size: int64(len(content))}, nil
}

func (f viewFS) Open(name string) (fs.File, error) {
	content, err := readThrough(name, f.fs)
	if err != nil {
		return nil, err
	}

	info := viewFileInfo{name: name, size: int64(len(content))}

	return &viewFile{Reader: bytes.NewReader(content), info: info}, nil
}

// readThrough reads name as an absolute OS path through src, surfacing
// read failures (missing files report their error via Content).
func readThrough(name string, src FileSource) ([]byte, error) {
	fh, err := src.ReadFile(context.Background(), uri.File(name))
	if err != nil {
		return nil, err
	}

	return fh.Content()
}

type viewFileInfo struct {
	name string
	size int64
}

func (i viewFileInfo) Name() string       { return i.name }
func (i viewFileInfo) Size() int64        { return i.size }
func (i viewFileInfo) Mode() fs.FileMode  { return 0o644 }
func (i viewFileInfo) ModTime() time.Time { return time.Time{} }
func (i viewFileInfo) IsDir() bool        { return false }
func (i viewFileInfo) Sys() any           { return nil }

type viewFile struct {
	*bytes.Reader
	info fs.FileInfo
}

func (f *viewFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *viewFile) Close() error               { return nil }

func BuildViewForTest(files []*FileChange) *View {
	return BuildViewForTestWithPaths(nil, files)
}

// BuildViewForTestWithPaths is BuildViewForTest with configured include
// paths, for cross-project include resolution tests.
func BuildViewForTestWithPaths(includePaths []string, files []*FileChange) *View {
	c := NewMemoizedFS()
	fs := NewOverlayFS(c)
	_ = fs.Update(context.TODO(), files)

	view := NewView("file:///tmp", fs, includePaths)

	for _, f := range files {
		_, _ = view.Parse(context.TODO(), f.URI)
	}

	return view
}
