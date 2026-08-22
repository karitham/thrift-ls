package cache

import (
	"bytes"
	"context"
	"io/fs"
	"strings"
	"time"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/options"
	"github.com/karitham/thrift-ls/resolver"
	"github.com/karitham/thrift-ls/syntax"
)

// Snapshot is a read handle over a view's current state. It exists so
// request handlers have a stable value to pass around; all reads see the
// view's live store, so nothing is copied or frozen.
//
// The gen field pins the generation the handle was taken at, for IsCurrent
// staleness checks.
type Snapshot struct {
	view         *View
	includePaths []string
	gen          uint64
}

// Snapshot returns a read handle over the view's current state plus a
// no-op release function.
func (v *View) Snapshot() (*Snapshot, func()) {
	ss := &Snapshot{
		view:         v,
		includePaths: v.includePaths,
		gen:          v.Generation(),
	}

	return ss, func() {}
}

// NewSnapshot returns a read handle over view, carrying an includePaths
// override for the handle's resolver.
func NewSnapshot(view *View, includePaths []string) *Snapshot {
	return &Snapshot{
		view:         view,
		includePaths: includePaths,
		gen:          view.Generation(),
	}
}

func (s *Snapshot) Includes(file uri.URI) []uri.URI {
	return s.view.Includes(file)
}

func (s *Snapshot) Includers(file uri.URI) []uri.URI {
	return s.view.Includers(file)
}

func (s *Snapshot) Dependents(uri uri.URI) []uri.URI {
	return s.view.Dependents(uri)
}

// View returns the view this snapshot serves: the workspace folder the
// snapshot resolves files under.
func (s *Snapshot) View() *View {
	return s.view
}

// Resolver returns a new Resolver instance for this snapshot.
// The resolver provides centralized include path resolution.
func (s *Snapshot) Resolver() *Resolver {
	return newResolver(s.includePaths, s.view.fs)
}

func (s *Snapshot) ReadFile(ctx context.Context, uri uri.URI) (FileHandle, error) {
	return s.view.ReadFile(ctx, uri)
}

func (s *Snapshot) Parse(ctx context.Context, uri uri.URI) (*ParsedFile, error) {
	return s.view.Parse(ctx, uri)
}

// TokensForFile returns the identifier tokens of file and its transitively
// included files. Each file's token set is computed once per parse and
// reused, so typing does not re-walk the include closure's ASTs.
func (s *Snapshot) TokensForFile(file uri.URI) map[string]struct{} {
	return s.view.TokensForFile(file)
}

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

// IncludePaths returns the include paths configured for this snapshot
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

func BuildSnapshotForTest(files []*FileChange) *Snapshot {
	return BuildSnapshotForTestWithPaths(nil, files)
}

// BuildSnapshotForTestWithPaths is BuildSnapshotForTest with configured
// include paths, for cross-project include resolution tests.
func BuildSnapshotForTestWithPaths(includePaths []string, files []*FileChange) *Snapshot {
	c := New()
	fs := NewOverlayFS(c)
	_ = fs.Update(context.TODO(), files)

	view := NewView("file:///tmp", fs, includePaths, options.Patch{})

	for _, f := range files {
		_, _ = view.Parse(context.TODO(), f.URI)
	}

	ss, _ := view.Snapshot()

	return ss
}
