// Package resolvertest provides an in-memory file tree for include
// resolution tests, so tests never touch disk. One seed serves every
// suite:
//
//	resolvertest.Seed("/base/shared.thrift")        // existence only
//	resolver.New(paths, resolver.WithFS(tree))      // fs.FS shape
//	resolver.New(paths, resolver.WithChecker(tree)) // Checker shape
//	cache.NewMemFS(tree.URIs())                     // FileSource shape
//
// The production path stays string paths end to end; URIs converts keys
// for FileSource-based suites only.
package resolvertest

import (
	"bytes"
	"context"
	"io/fs"
	"path"
	"time"

	"go.lsp.dev/uri"
)

// Map is an in-memory file tree keyed by path. Lookups are exact matches,
// so seed the same form the resolver produces (absolute paths joined from
// include roots). The zero value is an empty tree, ready for existence
// checks.
type Map map[string][]byte

var (
	_ fs.FS     = Map{}
	_ fs.StatFS = Map{}
)

// Seed builds a Map from paths with no content. Include resolution only
// probes existence, so content is irrelevant; tests asserting on content
// seed Map literals directly.
func Seed(paths ...string) Map {
	m := make(Map, len(paths))
	for _, p := range paths {
		m[p] = nil
	}

	return m
}

// Exists reports whether path is a seeded file, without reading content. It
// satisfies the resolver's existence checker contract.
func (m Map) Exists(ctx context.Context, path string) bool {
	if err := ctx.Err(); err != nil {
		return false
	}

	_, ok := m[path]

	return ok
}

// URIs converts the tree to a FileSource seed: each path becomes its file
// URI with the same content. The content slices are shared with the Map,
// so tests must not mutate them.
func (m Map) URIs() map[uri.URI][]byte {
	out := make(map[uri.URI][]byte, len(m))
	for p, content := range m {
		out[uri.File(p)] = content
	}

	return out
}

// Open implements fs.FS by exact path match.
func (m Map) Open(name string) (fs.File, error) {
	data, ok := m[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}

	return &file{Reader: bytes.NewReader(data), info: stat(name, data)}, nil
}

// Stat implements fs.StatFS by exact path match, so fs.Stat prefers it over
// opening the file.
func (m Map) Stat(name string) (fs.FileInfo, error) {
	data, ok := m[name]
	if !ok {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
	}

	return stat(name, data), nil
}

func stat(name string, data []byte) fs.FileInfo {
	return fileInfo{name: path.Base(name), size: int64(len(data))}
}

type fileInfo struct {
	name string
	size int64
}

func (i fileInfo) Name() string       { return i.name }
func (i fileInfo) Size() int64        { return i.size }
func (i fileInfo) Mode() fs.FileMode  { return 0 }
func (i fileInfo) ModTime() time.Time { return time.Time{} }
func (i fileInfo) IsDir() bool        { return false }
func (i fileInfo) Sys() any           { return nil }

type file struct {
	*bytes.Reader
	info fs.FileInfo
}

func (f *file) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *file) Close() error               { return nil }
