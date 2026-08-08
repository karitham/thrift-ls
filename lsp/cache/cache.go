package cache

type Cache struct {
	IncludePaths []string

	*memoizedFS
}

func New(includePaths []string) *Cache {
	return &Cache{
		IncludePaths: includePaths,
		memoizedFS:   &memoizedFS{filesByID: map[FileID][]*DiskFile{}},
	}
}
