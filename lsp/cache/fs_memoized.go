package cache

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.lsp.dev/uri"
)

// A memoizedFS is a file source that memoizes reads, to reduce IO.
type memoizedFS struct {
	mu sync.Mutex

	// filesByID maps existing file inodes to the result of a read.
	// (The read may have failed, e.g. due to EACCES or a delete between stat+read.)
	// Each slice is a non-empty list of aliases: different URIs.
	filesByID map[FileID][]*DiskFile
}

// NewMemoizedFS returns the production disk file source: reads stat first
// and memoize by inode+mtime.
func NewMemoizedFS() FileSource {
	return &memoizedFS{filesByID: map[FileID][]*DiskFile{}}
}

// A DiskFile is a file on the filesystem, or a failure to read one.
// It implements the source.FileHandle interface.
type DiskFile struct {
	uri     uri.URI
	modTime time.Time
	content []byte
	err     error
}

func (h *DiskFile) URI() uri.URI { return h.uri }

func (h *DiskFile) Version() int32           { return 0 }
func (h *DiskFile) Content() ([]byte, error) { return h.content, h.err }

// WalkFiles calls fn for every file under root in lexical order. Entries
// that fail to stat are skipped: one unreadable or deleted file must not
// kill the walk.
func (m *memoizedFS) WalkFiles(ctx context.Context, root uri.URI, fn func(uri.URI) error) error {
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

// ReadFile stats and (maybe) reads the file, updates the cache, and returns it.
func (fs *memoizedFS) ReadFile(ctx context.Context, uri uri.URI) (FileHandle, error) {
	id, mtime, err := GetFileID(uri.FsPath())
	if err != nil {
		// file does not exist
		return &DiskFile{
			err: err,
			uri: uri,
		}, nil
	}

	// We check if the file has changed by comparing modification times. Notably,
	// this is an imperfect heuristic as various systems have low resolution
	// mtimes (as much as 1s on WSL or s390x builders), so we only cache
	// filehandles if mtime is old enough to be reliable, meaning that we don't
	// expect a subsequent write to have the same mtime.
	//
	// The coarsest mtime precision we've seen in practice is 1s, so consider
	// mtime to be unreliable if it is less than 2s old. Capture this before
	// doing anything else.
	recentlyModified := time.Since(mtime) < 2*time.Second

	fs.mu.Lock()

	fhs, ok := fs.filesByID[id]
	if ok && fhs[0].modTime.Equal(mtime) {
		var fh *DiskFile
		// We have already seen this file and it has not changed.
		for _, h := range fhs {
			if h.uri == uri {
				fh = h

				break
			}
		}
		// No file handle for this exact URI. Create an alias, but share content.
		if fh == nil {
			newFH := *fhs[0]
			newFH.uri = uri
			fh = &newFH
			fhs = append(fhs, fh)
			fs.filesByID[id] = fhs
		}
		fs.mu.Unlock()

		return fh, nil
	}
	fs.mu.Unlock()

	// Unknown file, or file has changed. Read (or re-read) it.
	fh, err := readFile(ctx, uri, mtime) // ~25us
	if err != nil {
		return nil, err // e.g. cancelled (not: read failed)
	}

	fs.mu.Lock()
	if !recentlyModified {
		fs.filesByID[id] = []*DiskFile{fh}
	} else {
		delete(fs.filesByID, id)
	}
	fs.mu.Unlock()

	return fh, nil
}

// ioLimit limits the number of parallel file reads per process.
var ioLimit = make(chan struct{}, 128)

func readFile(ctx context.Context, uri uri.URI, mtime time.Time) (*DiskFile, error) {
	select {
	case ioLimit <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	defer func() { <-ioLimit }()

	// It is possible that a race causes us to read a file with different file
	// ID, or whose mtime differs from the given mtime. However, in these cases
	// we expect the client to notify of a subsequent file change, and the file
	// content should be eventually consistent.
	// FsPath, not Path: Path keeps the leading slash before a Windows drive
	// letter ("/c:/dir/x.thrift"), which Win32 rejects with an invalid-name
	// error.
	content, err := os.ReadFile(uri.FsPath()) // ~20us
	if err != nil {
		content = nil // just in case
	}

	return &DiskFile{
		modTime: mtime,
		uri:     uri,
		content: content,
		err:     err,
	}, nil
}
