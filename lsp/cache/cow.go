package cache

import (
	"maps"
	"sync"

	"go.lsp.dev/uri"
)

// cowMap is a copy-on-write map. Snapshots share the underlying map
// (Clone is O(1)); the first write after a clone copies the map, so
// cloning per keystroke is cheap while old snapshots stay immutable.
type cowMap[K comparable, V any] struct {
	mu     sync.RWMutex
	m      map[K]V
	shared bool
}

func newCowMap[K comparable, V any]() *cowMap[K, V] {
	return &cowMap[K, V]{m: make(map[K]V)}
}

func (m *cowMap[K, V]) Get(key K) (V, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	v, ok := m.m[key]

	return v, ok
}

func (m *cowMap[K, V]) Set(key K, val V) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.copyOnWrite()

	m.m[key] = val
}

func (m *cowMap[K, V]) Forget(key K) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.copyOnWrite()

	delete(m.m, key)
}

// Clone returns a map sharing the same entries. The clone and the original
// both become copy-on-write.
func (m *cowMap[K, V]) Clone() *cowMap[K, V] {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.shared = true

	return &cowMap[K, V]{m: m.m, shared: true}
}

// copyOnWrite detaches the map from a shared parent before the first write.
// Callers must hold mu.
func (m *cowMap[K, V]) copyOnWrite() {
	if !m.shared {
		return
	}

	m2 := make(map[K]V, len(m.m)+1)
	maps.Copy(m2, m.m)

	m.m = m2
	m.shared = false
}

// ParseCaches maps URIs to parsed files.
type ParseCaches = cowMap[uri.URI, *ParsedFile]

// NewParseCaches returns an empty parse cache.
func NewParseCaches() *ParseCaches {
	return newCowMap[uri.URI, *ParsedFile]()
}

// FilesMap holds the files of a snapshot.
type FilesMap = cowMap[uri.URI, FileHandle]

// NewFilesMap returns an empty files map.
func NewFilesMap() *FilesMap {
	return newCowMap[uri.URI, FileHandle]()
}
