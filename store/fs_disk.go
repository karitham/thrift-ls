package store

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"

	"go.lsp.dev/uri"
)

// diskFS is the production disk file source: every read hits the disk.
// There is deliberately no memoization. Views cache parses, so a disk
// read happens at most once per file per change; caching here would only
// risk serving stale content within the same mtime tick. Thrift
// workspaces are hundreds of small files and a read is microseconds.
type diskFS struct{}

// NewDiskFS returns the production disk file source.
func NewDiskFS() FileSource { return diskFS{} }

// diskFile is a file on the filesystem, or a failure to read one.
// It implements the FileHandle interface.
type diskFile struct {
	uri     uri.URI
	content []byte
	err     error
}

func (h *diskFile) URI() uri.URI { return h.uri }

func (h *diskFile) Version() int32           { return 0 }
func (h *diskFile) Content() ([]byte, error) { return h.content, h.err }

// Exists reports whether path names an existing regular file, without
// reading content. It stats only, so include probing never pulls file
// bodies into memory.
func (diskFS) Exists(ctx context.Context, path string) bool {
	if err := ctx.Err(); err != nil {
		return false
	}

	st, err := os.Stat(path)
	if err != nil {
		return false
	}

	return !st.IsDir()
}

// WalkFiles calls fn for every file under root in lexical order. Entries
// that fail to stat are skipped: one unreadable or deleted file must not
// kill the walk.
func (diskFS) WalkFiles(_ context.Context, root uri.URI, fn func(uri.URI) error) error {
	return filepath.WalkDir(root.FsPath(), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entry: keep walking
		}

		if d.IsDir() {
			return nil
		}

		return fn(uri.File(path))
	})
}

// ReadFile reads the file from disk. A missing or unreadable file is not
// a call failure: the returned handle carries the error, so one bad path
// never fails a workspace walk.
func (diskFS) ReadFile(_ context.Context, u uri.URI) (FileHandle, error) {
	// FsPath, not Path: Path keeps the leading slash before a Windows
	// drive letter ("/c:/dir/x.thrift"), which Win32 rejects with an
	// invalid-name error.
	content, err := os.ReadFile(u.FsPath())
	if err != nil {
		content = nil // just in case
	}

	return &diskFile{uri: u, content: content, err: err}, nil
}
