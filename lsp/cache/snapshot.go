package cache

import (
	"context"
	"math/rand"
	"strings"
	"sync"

	"github.com/joyme123/thrift-ls/lsp/memoize"
	"github.com/joyme123/thrift-ls/parser"
	"github.com/joyme123/thrift-ls/resolver"
	log "github.com/sirupsen/logrus"
	"go.lsp.dev/uri"
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
		central: resolver.New(ss.includePaths),
	}
}

// IncludePaths returns the include paths configured for this snapshot
func (r *Resolver) IncludePaths() []string {
	return r.ss.includePaths
}

// ResolveInclude resolves an include path to a file URI.
// It first tries relative to the current file, then tries each include path.
func (r *Resolver) ResolveInclude(cur uri.URI, includePath string) uri.URI {
	filePath := cur.Filename()
	resolvedPath := r.central.Resolve(filePath, includePath)
	return uri.File(resolvedPath)
}

// ResolveIncludeWithText resolves an include path using the raw text from the AST.
// This is more efficient when the include text is already available.
func (r *Resolver) ResolveIncludeWithText(cur uri.URI, includeText string) uri.URI {
	return r.ResolveInclude(cur, includeText)
}

// GetIncludePath returns the include path text for a given include name.
// Returns empty string if not found.
func (r *Resolver) GetIncludePath(ast *parser.Document, includeName string) string {
	for _, include := range ast.Includes {
		if include.BadNode || include.Path == nil || include.Path.BadNode || include.Path.Value == nil {
			continue
		}
		path := include.Path.Value.Text
		name := getIncludeNameFromPath(path)
		if name == includeName {
			return path
		}
	}
	return ""
}

// GetIncludeURI returns the URI for an included file by include name.
// Returns empty URI if not found.
func (r *Resolver) GetIncludeURI(cur uri.URI, ast *parser.Document, includeName string) uri.URI {
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
	id int64

	view *View

	// ctx is used to cancel background job
	ctx context.Context

	refCount sync.WaitGroup

	files *FilesMap

	store *memoize.Store

	graph       *IncludeGraph
	parsedCache *ParseCaches

	includePaths []string
}

func NewSnapshot(view *View, store *memoize.Store, includePaths []string) *Snapshot {
	snapshot := &Snapshot{
		id:          rand.Int63(),
		view:        view,
		store:       store,
		ctx:         context.Background(),
		refCount:    sync.WaitGroup{},
		graph:       NewIncludeGraph(),
		parsedCache: NewParseCaches(),
		files: &FilesMap{
			files:    make(map[uri.URI]FileHandle),
			overlays: make(map[uri.URI]*Overlay),
		},
		includePaths: includePaths,
	}

	return snapshot
}

func (s *Snapshot) Acquire() func() {
	s.refCount.Add(1)
	return s.refCount.Done
}

func (s *Snapshot) Initialize(ctx context.Context) {

}

func (s *Snapshot) Graph() *IncludeGraph {
	return s.graph
}

// Resolver returns a new Resolver instance for this snapshot.
// The resolver provides centralized include path resolution.
func (s *Snapshot) Resolver() *Resolver {
	return NewResolver(s)
}

func (s *Snapshot) ReadFile(ctx context.Context, uri uri.URI) (FileHandle, error) {
	log.Debugln("snapshot read file", uri)
	s.view.MarkFileKnown(uri)

	if fh, ok := s.files.Get(uri); ok {
		return fh, nil
	}

	log.Debugln("snapshot read from fs")
	fh, err := s.view.fs.ReadFile(ctx, uri)
	if err != nil {
		return nil, err
	}
	s.files.Set(uri, fh)

	return fh, nil
}

// ForgetFile is called when file changed or removed
// it remove file cache and parsed cache
func (s *Snapshot) ForgetFile(uri uri.URI) {
	s.files.Forget(uri)
	s.graph.Remove(uri)
	s.parsedCache.Forget(uri)
}

func (s *Snapshot) Parse(ctx context.Context, uri uri.URI) (*ParsedFile, error) {
	if parsedFile := s.parsedCache.Get(uri); parsedFile != nil {
		return parsedFile, nil
	}

	fh, err := s.ReadFile(ctx, uri)
	if err != nil {
		return nil, err
	}

	// DEBUG
	// content, _ := fh.Content()
	// log.Debugln("parse content:", string(content))

	pf, err := Parse(fh)
	if err != nil {
		log.Debugf("snapshot parse err: %v", err)
		return nil, err
	}

	if pf.AST() != nil {
		s.graph.Set(uri, pf.AST().Includes, s.includePaths)
	}
	s.parsedCache.Set(uri, pf)

	return pf, nil
}

func (s *Snapshot) Tokens() map[string]struct{} {
	return s.parsedCache.Tokens()
}

func (s *Snapshot) TokensForFile(file uri.URI) map[string]struct{} {
	return s.parsedCache.TokensForFile(file, func(f uri.URI) []uri.URI {
		node := s.graph.Get(f)
		if node == nil {
			return nil
		}
		return node.OutDegree()
	})
}

func (s *Snapshot) clone() (*Snapshot, func()) {
	snap := &Snapshot{
		id:   rand.Int63(),
		view: s.view,
		ctx:  context.Background(),
		// TODO(jpf): file change 没有更新，导致读到旧的缓存
		files: s.files.Clone(),
		// files: &FilesMap{
		// 	files:    make(map[uri.URI]FileHandle),
		// 	overlays: make(map[uri.URI]*Overlay),
		// },
		graph:        s.graph.Clone(),
		parsedCache:  s.parsedCache.Clone(),
		includePaths: s.includePaths,
	}

	return snap, snap.Acquire()
}

func BuildSnapshotForTest(files []*FileChange) *Snapshot {
	store := &memoize.Store{}
	c := New(store, nil)
	fs := NewOverlayFS(c)
	fs.Update(context.TODO(), files)

	view := NewView("test", "file:///tmp", fs, store, nil)
	ss := NewSnapshot(view, store, nil)

	for _, f := range files {
		ss.Parse(context.TODO(), f.URI)
	}

	return ss
}
