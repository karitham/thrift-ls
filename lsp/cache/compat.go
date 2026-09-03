package cache

// Temporary aliases for the file sources extracted to vfs. They keep the
// tree green while the document store moves to store; step 2 deletes this
// file and rewrites importers to vfs directly.
import "github.com/karitham/thrift-ls/vfs"

type (
	FileSource     = vfs.FileSource
	FileHandle     = vfs.FileHandle
	Checker        = vfs.Checker
	FileID         = vfs.FileID
	DiskFile       = vfs.DiskFile
	Overlay        = vfs.Overlay
	OverlayFS      = vfs.OverlayFS
	overlayFS      = vfs.OverlayFS
	FileChange     = vfs.FileChange
	FileChangeType = vfs.FileChangeType
)

const (
	FileChangeTypeInitialize = vfs.FileChangeTypeInitialize
	FileChangeTypeDidOpen    = vfs.FileChangeTypeDidOpen
	FileChangeTypeDidChange  = vfs.FileChangeTypeDidChange
	FileChangeTypeDidClose   = vfs.FileChangeTypeDidClose
)

var (
	NewMemFS      = vfs.NewMemFS
	NewMemoizedFS = vfs.NewMemoizedFS
	NewOverlayFS  = vfs.NewOverlayFS
	NewOverlay    = vfs.NewOverlay
	GetFileID     = vfs.GetFileID
)
