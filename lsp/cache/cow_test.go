package cache

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.lsp.dev/uri"
)

// gundamSnapshotFiles is a two-file corpus: strike_rouge includes
// federation.gundam.
func gundamSnapshotFiles() []*FileChange {
	return []*FileChange{
		{
			URI: uri.URI("file:///tmp/strike_rouge.thrift"),
			Content: []byte(`include "federation.gundam.thrift"

exception BayFull {
	1: string message
}`),
			From: FileChangeTypeDidOpen,
		},
		{
			URI: uri.URI("file:///tmp/federation.gundam.thrift"),
			Content: []byte(`struct Gundam {
	1: required string Name
}`),
			From: FileChangeTypeDidOpen,
		},
	}
}

// TestSnapshotCloneIsolation: snapshot clones share maps copy-on-write, so
// mutating a clone must never leak into the snapshot it was cloned from.
func TestSnapshotCloneIsolation(t *testing.T) {
	ss := BuildSnapshotForTest(gundamSnapshotFiles())

	clone, release := ss.clone()
	defer release()

	// Mutate the clone: replace the file's content and forget its caches.
	clone.files.Set("file:///tmp/federation.gundam.thrift", NewOverlay(
		"file:///tmp/federation.gundam.thrift",
		[]byte("struct Gundam {\n\t1: required string Name,\n\t2: optional i32 SerialNumber\n}"),
		2,
	))
	clone.parsedCache.Forget("file:///tmp/federation.gundam.thrift")
	clone.context.Forget("file:///tmp/federation.gundam.thrift")

	// The clone sees the new content; the original keeps the old.
	clonePf, err := clone.Parse(t.Context(), "file:///tmp/federation.gundam.thrift")
	assert.NoError(t, err)
	assert.Len(t, clonePf.AST().Structs()[0].Fields, 2, "clone parses the new content")

	origPf := ss.parsedCache.Get("file:///tmp/federation.gundam.thrift")
	assert.NotNil(t, origPf, "original snapshot keeps its parsed file")
	assert.Len(t, origPf.AST().Structs()[0].Fields, 1, "original snapshot is unaffected by clone writes")

	// The original snapshot's graph is untouched by the clone's Forget.
	assert.Equal(t, []uri.URI{"file:///tmp/strike_rouge.thrift"}, ss.Dependents("file:///tmp/federation.gundam.thrift"))
}

// BenchmarkSnapshotClone shows that cloning a snapshot with many parsed
// files is O(1): the maps are shared copy-on-write.
func BenchmarkSnapshotClone(b *testing.B) {
	files := make([]*FileChange, 0, 100)
	for i := range 100 {
		files = append(files, &FileChange{
			URI:     uri.URI(fmt.Sprintf("file:///tmp/bench%d.thrift", i)),
			Content: []byte("struct Gundam {\n\t1: required string Name\n}"),
			From:    FileChangeTypeDidOpen,
		})
	}

	ss := BuildSnapshotForTest(files)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, release := ss.clone()
		release()
	}
}
