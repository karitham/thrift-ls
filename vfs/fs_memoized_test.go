package vfs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/uri"
)

// writeWithMtime creates path with content and pins its mtime more than
// the 2s freshness window in the past, so memoization decisions do not
// depend on test timing.
func writeWithMtime(t *testing.T, path, content string) {
	t.Helper()

	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	mtime := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(path, mtime, mtime))
}

// TestMemoizedFS_MemoHit pins the core contract: two reads of an
// unchanged file return the same cached handle.
func TestMemoizedFS_MemoHit(t *testing.T) {
	fs := &memoizedFS{filesByID: map[FileID][]*DiskFile{}}
	ctx := context.Background()

	path := filepath.Join(t.TempDir(), "a.thrift")
	writeWithMtime(t, path, "struct A {}")

	u := uri.File(path)

	first, err := fs.ReadFile(ctx, u)
	require.NoError(t, err)

	content, err := first.Content()
	require.NoError(t, err)
	assert.Equal(t, "struct A {}", string(content))

	second, err := fs.ReadFile(ctx, u)
	require.NoError(t, err)

	assert.Same(t, first.(*DiskFile), second.(*DiskFile), "unchanged file must reuse the cached handle")
}

// TestMemoizedFS_MtimeInvalidation pins the behavior the freshness
// heuristic exists for: when the mtime changes, the stale handle is
// dropped even though the inode is the same.
func TestMemoizedFS_MtimeInvalidation(t *testing.T) {
	fs := &memoizedFS{filesByID: map[FileID][]*DiskFile{}}
	ctx := context.Background()

	path := filepath.Join(t.TempDir(), "a.thrift")
	u := uri.File(path)

	writeWithMtime(t, path, "before")

	before, err := fs.ReadFile(ctx, u)
	require.NoError(t, err)

	content, err := before.Content()
	require.NoError(t, err)
	assert.Equal(t, "before", string(content))

	writeWithMtime(t, path, "after")

	after, err := fs.ReadFile(ctx, u)
	require.NoError(t, err)

	content, err = after.Content()
	require.NoError(t, err)
	assert.Equal(t, "after", string(content))

	assert.NotSame(t, before.(*DiskFile), after.(*DiskFile))
}

// TestMemoizedFS_MissingFile pins that reads of nonexistent files return
// a handle carrying the error instead of failing the call: one bad path
// must not fail a workspace walk.
func TestMemoizedFS_MissingFile(t *testing.T) {
	fs := &memoizedFS{filesByID: map[FileID][]*DiskFile{}}

	u := uri.File(filepath.Join(t.TempDir(), "ghost.thrift"))

	fh, err := fs.ReadFile(context.Background(), u)
	require.NoError(t, err)

	_, cerr := fh.Content()
	require.Error(t, cerr)
}
