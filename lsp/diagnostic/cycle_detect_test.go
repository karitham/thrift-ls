package diagnostic

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/memoize"
	"github.com/karitham/thrift-ls/syntax"
)

func Test_cycleDetect(t *testing.T) {
	includesMap := map[uri.URI][]Include{
		"/user.thrift": {
			Include{file: "/goods.thrift"},
			Include{file: "/address.thrift"},
		},
		"/goods.thrift":   {Include{file: "/user.thrift"}},
		"/address.thrift": {Include{file: "/user.thrift"}},
	}

	type args struct {
		includesMap *map[uri.URI][]Include
	}
	tests := []struct {
		name string
		args args
		want []CyclePair
	}{
		{
			name: "cycle",
			args: args{
				includesMap: &includesMap,
			},
			want: []CyclePair{
				{
					file: "/user.thrift",
					include: Include{
						file: "/goods.thrift",
					},
				},
				{
					file:    "/goods.thrift",
					include: Include{file: "/user.thrift"},
				},
				{
					file:    "/user.thrift",
					include: Include{file: "/address.thrift"},
				},
				{
					file:    "/address.thrift",
					include: Include{file: "/user.thrift"},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sort.SliceStable(tt.want, func(i, j int) bool {
				if tt.want[i].file == tt.want[j].file {
					return tt.want[i].include.file < tt.want[j].include.file
				}
				return tt.want[i].file < tt.want[j].file
			})

			got := cycleDetect(tt.args.includesMap)
			sort.SliceStable(got, func(i, j int) bool {
				if got[i].file == got[j].file {
					return got[i].include.file < got[j].include.file
				}
				return got[i].file < got[j].file
			})

			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_getIncludes(t *testing.T) {
	file1 := `include "./test/goods.thrift"
include "./test/address.thrift"`
	file2 := `include "../user.thrift"`
	file3 := `include "../user.thrift"`

	ss := buildSnapshotForTest(t, []*cache.FileChange{
		{
			URI:     "file:///tmp/user.thrift",
			Version: 0,
			Content: []byte(file1),
			From:    cache.FileChangeTypeDidOpen,
		},
		{
			URI:     "file:///tmp/test/goods.thrift",
			Version: 0,
			Content: []byte(file2),
			From:    cache.FileChangeTypeDidOpen,
		},
		{
			URI:     "file:///tmp/test/address.thrift",
			Version: 0,
			Content: []byte(file3),
			From:    cache.FileChangeTypeDidOpen,
		},
	})

	// Expected includes, built by parsing the same sources.
	parseFor := func(uriStr, src string) *syntax.Document {
		doc, errs := syntax.Parse([]byte(src))
		for _, e := range errs {
			if e.Severity == syntax.SeverityError {
				t.Fatal(e)
			}
		}
		return doc
	}
	userDoc := parseFor("file:///tmp/user.thrift", file1)
	goodsDoc := parseFor("file:///tmp/test/goods.thrift", file2)
	addressDoc := parseFor("file:///tmp/test/address.thrift", file3)

	expectIncludeMap := map[uri.URI][]Include{
		"file:///tmp/user.thrift": {
			Include{file: "file:///tmp/test/goods.thrift", include: userDoc.Includes()[0], doc: userDoc},
			Include{file: "file:///tmp/test/address.thrift", include: userDoc.Includes()[1], doc: userDoc},
		},
		"file:///tmp/test/goods.thrift": {
			Include{file: "file:///tmp/user.thrift", include: goodsDoc.Includes()[0], doc: goodsDoc},
		},
		"file:///tmp/test/address.thrift": {
			Include{file: "file:///tmp/user.thrift", include: addressDoc.Includes()[0], doc: addressDoc},
		},
	}

	includeMap := make(map[uri.URI][]Include)

	type args struct {
		ctx         context.Context
		ss          *cache.Snapshot
		file        uri.URI
		includesMap *map[uri.URI][]Include
	}
	tests := []struct {
		name      string
		args      args
		want      *map[uri.URI][]Include
		assertion assert.ErrorAssertionFunc
	}{
		{
			name: "normal",
			args: args{
				ctx:         t.Context(),
				ss:          ss,
				file:        "file:///tmp/user.thrift",
				includesMap: &includeMap,
			},
			want:      &expectIncludeMap,
			assertion: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.assertion(t, getIncludes(tt.args.ctx, tt.args.ss, tt.args.file, tt.args.includesMap))

			assert.Equal(t, tt.want, tt.args.includesMap)
		})
	}
}

func buildSnapshotForTest(t *testing.T, files []*cache.FileChange) *cache.Snapshot {
	store := &memoize.Store{}
	c := cache.New(store, nil)
	fs := cache.NewOverlayFS(c)
	_ = fs.Update(t.Context(), files)

	view := cache.NewView("test", "file:///tmp", fs, store, nil)
	ss := cache.NewSnapshot(view, store, nil)

	return ss
}
