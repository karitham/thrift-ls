package cache

import (
	"bytes"
	"context"
	"io/fs"
	"log/slog"
	"strings"
	"sync"
	"time"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/options"
	"github.com/karitham/thrift-ls/resolver"
	"github.com/karitham/thrift-ls/syntax"
)

// Resolver provides centralized include path resolution.
// It wraps the snapshot to provide a clean interface for resolving
// included files, types, and identifiers.
type Resolver struct {
	ss      *Snapshot
	central *resolver.Resolver
}

// NewResolver creates a resolver for the given snapshot
func NewResolver(ss *Snapshot) *Resolver {
	return &Resolver{
		ss:      ss,
		central: resolver.NewWithFS(ss.includePaths, &snapshotFS{ss: ss}),
	}
}

// snapshotFS is an fs.FS for include resolution: files open in the snapshot
// (editor overlays) exist first, everything else falls through to the disk.
// This lets includes resolve for files that are open but not yet saved.
type snapshotFS struct {
	ss   *Snapshot
	disk fs.FS
}

func (s *snapshotFS) Stat(name string) (fs.FileInfo, error) {
	if _, ok := s.ss.files.Get(uri.File(name)); ok {
		return snapshotFileInfo{}, nil
	}

	if s.disk == nil {
		s.disk = resolver.FS()
	}

	return fs.Stat(s.disk, name)
}

func (s *snapshotFS) Open(name string) (fs.File, error) {
	if fh, ok := s.ss.files.Get(uri.File(name)); ok {
		content, err := fh.Content()
		if err != nil {
			return nil, err
		}

		return &snapshotFile{Reader: bytes.NewReader(content), info: snapshotFileInfo{}}, nil
	}

	if s.disk == nil {
		s.disk = resolver.FS()
	}

	return s.disk.Open(name)
}

type snapshotFileInfo struct{}

func (snapshotFileInfo) Name() string       { return "" }
func (snapshotFileInfo) Size() int64        { return 0 }
func (snapshotFileInfo) Mode() fs.FileMode  { return 0 }
func (snapshotFileInfo) ModTime() time.Time { return time.Time{} }
func (snapshotFileInfo) IsDir() bool        { return false }
func (snapshotFileInfo) Sys() any           { return nil }

type snapshotFile struct {
	*bytes.Reader
	info fs.FileInfo
}

func (f *snapshotFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *snapshotFile) Close() error               { return nil }

// IncludePaths returns the include paths configured for this snapshot
func (r *Resolver) IncludePaths() []string {
	return r.ss.includePaths
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

type Snapshot struct {
	view *View

	refCount sync.WaitGroup

	files *FilesMap

	context     *IncludeDeps
	parsedCache *ParseCaches

	includePaths []string
}

func NewSnapshot(view *View, includePaths []string) *Snapshot {
	snapshot := &Snapshot{
		view: view,

		refCount:     sync.WaitGroup{},
		context:      NewIncludeDeps(),
		parsedCache:  NewParseCaches(),
		files:        NewFilesMap(),
		includePaths: includePaths,
	}

	return snapshot
}

func (s *Snapshot) Acquire() func() {
	s.refCount.Add(1)

	return s.refCount.Done
}

// Includes returns the files file includes directly, in include order.
func (s *Snapshot) Includes(file uri.URI) []uri.URI {
	return s.context.Includes(file)
}

// Includers returns the files that include file directly, in graph order.
func (s *Snapshot) Includers(file uri.URI) []uri.URI {
	return s.context.Includers(file)
}

// Dependents returns the transitive dependents of uri: every file that
// directly or transitively includes it, in this snapshot.
func (s *Snapshot) Dependents(uri uri.URI) []uri.URI {
	return s.context.Dependents(uri)
}

// View returns the view this snapshot serves: the workspace folder the
// snapshot resolves files under.
func (s *Snapshot) View() *View {
	return s.view
}

// Resolver returns a new Resolver instance for this snapshot.
// The resolver provides centralized include path resolution.
func (s *Snapshot) Resolver() *Resolver {
	return NewResolver(s)
}

func (s *Snapshot) ReadFile(ctx context.Context, uri uri.URI) (FileHandle, error) {
	slog.Debug("snapshot read file", "uri", uri)
	s.view.MarkFileKnown(uri)

	if fh, ok := s.files.Get(uri); ok {
		return fh, nil
	}

	slog.Debug("snapshot read from fs")

	fh, err := s.view.fs.ReadFile(ctx, uri)
	if err != nil {
		return nil, err
	}

	s.files.Set(uri, fh)

	return fh, nil
}

// ForgetFile is called when file changed or removed. It removes file's
// include edges and drops the parse and file caches for file and every
// transitive dependent of file: their derived data is rebuilt lazily on the
// next request, while their content survives on disk (or in the overlay).
func (s *Snapshot) ForgetFile(uri uri.URI) {
	s.files.Forget(uri)
	s.parsedCache.Forget(uri)

	for _, dependent := range s.context.Forget(uri) {
		s.files.Forget(dependent)
		s.parsedCache.Forget(dependent)
	}
}

func (s *Snapshot) Parse(ctx context.Context, uri uri.URI) (*ParsedFile, error) {
	if parsedFile, ok := s.parsedCache.Get(uri); ok {
		return parsedFile, nil
	}

	fh, err := s.ReadFile(ctx, uri)
	if err != nil {
		return nil, err
	}

	pf, err := Parse(fh)
	if err != nil {
		slog.Debug("snapshot parse failed", "err", err)

		return nil, err
	}

	if pf.AST() != nil {
		s.context.Register(uri, pf.AST().Includes(), s.Resolver().ResolveInclude)
	}

	s.parsedCache.Set(uri, pf)

	return pf, nil
}

// TokensForFile returns the identifier tokens of file and its transitively
// included files. Each file's token set is computed once per parse and
// reused, so typing does not re-walk the include closure's ASTs.
func (s *Snapshot) TokensForFile(file uri.URI) map[string]struct{} {
	tokens := make(map[string]struct{})
	visited := make(map[uri.URI]bool)

	var collect func(f uri.URI)

	collect = func(f uri.URI) {
		if visited[f] {
			return
		}

		visited[f] = true

		if pf, ok := s.parsedCache.Get(f); ok {
			for token := range pf.Tokens() {
				tokens[token] = struct{}{}
			}
		}

		for _, inc := range s.Includes(f) {
			collect(inc)
		}
	}

	collect(file)

	return tokens
}

func (s *Snapshot) clone() (*Snapshot, func()) {
	snap := &Snapshot{
		view:         s.view,
		files:        s.files.Clone(),
		context:      s.context.Clone(),
		parsedCache:  s.parsedCache.Clone(),
		includePaths: s.includePaths,
	}

	return snap, snap.Acquire()
}

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
	ss := NewSnapshot(view, includePaths)

	for _, f := range files {
		_, _ = ss.Parse(context.TODO(), f.URI)
	}

	return ss
}
