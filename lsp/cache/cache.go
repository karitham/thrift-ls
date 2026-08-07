package cache

import (
	"strconv"
	"sync/atomic"
)

type Cache struct {
	id string

	IncludePaths []string

	*memoizedFS
}

var cacheIndex int64

func New(includePaths []string) *Cache {
	index := atomic.AddInt64(&cacheIndex, 1)

	c := &Cache{
		id:           strconv.FormatInt(index, 10),
		IncludePaths: includePaths,
		memoizedFS:   &memoizedFS{filesByID: map[FileID][]*DiskFile{}},
	}

	return c
}

func (c *Cache) ID() string { return c.id }
