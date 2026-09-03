package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/uri"
)

// TestDiskFS_ReadsContent pins the whole contract: reads hit the disk
// and return the current content.
func TestDiskFS_ReadsContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.thrift")
	require.NoError(t, os.WriteFile(path, []byte("struct A {}"), 0o600))

	fh, err := NewDiskFS().ReadFile(context.Background(), uri.File(path))
	require.NoError(t, err)

	content, err := fh.Content()
	require.NoError(t, err)
	assert.Equal(t, "struct A {}", string(content))
}

// TestDiskFS_MissingFile pins that reads of nonexistent files return a
// handle carrying the error instead of failing the call: one bad path
// must not fail a workspace walk.
func TestDiskFS_MissingFile(t *testing.T) {
	u := uri.File(filepath.Join(t.TempDir(), "ghost.thrift"))

	fh, err := NewDiskFS().ReadFile(context.Background(), u)
	require.NoError(t, err)

	_, cerr := fh.Content()
	require.Error(t, cerr)
}

// TestDiskFS_Exists stats without reading: regular files exist,
// directories and missing paths do not.
func TestDiskFS_Exists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.thrift")
	require.NoError(t, os.WriteFile(path, []byte("struct A {}"), 0o600))

	fs := NewDiskFS()
	ctx := context.Background()

	assert.True(t, fs.(Checker).Exists(ctx, path))
	assert.False(t, fs.(Checker).Exists(ctx, dir))
	assert.False(t, fs.(Checker).Exists(ctx, filepath.Join(dir, "ghost.thrift")))
}
