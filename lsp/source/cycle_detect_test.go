package source

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
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

// Test_cycleDetectN pins cycle detection on arbitrary-length cycles: an
// include edge X -> Y closes a cycle when Y transitively includes X, no
// matter the cycle length. The existing 2-cycle case is covered in
// Test_cycleDetect; these cases exercise longer cycles, self-includes, and
// acyclic graphs.
func Test_cycleDetectN(t *testing.T) {
	// K-On themed include graph: the band's songs include each other's
	// tabs, and the clubroom includes everything.
	const (
		tea  = "/songs/tea_time.thrift"
		git  = "/songs/gitah.thrift"
		bass = "/songs/mio.thrift"
		drum = "/songs/ritsu.thrift"
		club = "/clubroom.thrift"
	)

	include := func(from uri.URI, tos ...uri.URI) map[uri.URI][]Include {
		m := make(map[uri.URI][]Include, len(tos))
		for _, to := range tos {
			m[from] = append(m[from], Include{file: to})
		}

		return m
	}

	tests := []struct {
		name  string
		graph map[uri.URI][]Include
		want  []CyclePair
	}{
		{
			name: "acyclic chain",
			graph: map[uri.URI][]Include{
				tea:  {Include{file: git}},
				git:  {Include{file: bass}},
				bass: {},
			},
			want: nil,
		},
		{
			name: "2-cycle",
			graph: map[uri.URI][]Include{
				tea: {Include{file: git}},
				git: {Include{file: tea}},
			},
			want: []CyclePair{
				{file: tea, include: Include{file: git}},
				{file: git, include: Include{file: tea}},
			},
		},
		{
			name: "3-cycle",
			graph: map[uri.URI][]Include{
				tea:  {Include{file: git}},
				git:  {Include{file: bass}},
				bass: {Include{file: tea}},
			},
			want: []CyclePair{
				{file: tea, include: Include{file: git}},
				{file: git, include: Include{file: bass}},
				{file: bass, include: Include{file: tea}},
			},
		},
		{
			name: "4-cycle",
			graph: map[uri.URI][]Include{
				tea:  {Include{file: git}},
				git:  {Include{file: bass}},
				bass: {Include{file: drum}},
				drum: {Include{file: tea}},
			},
			want: []CyclePair{
				{file: tea, include: Include{file: git}},
				{file: git, include: Include{file: bass}},
				{file: bass, include: Include{file: drum}},
				{file: drum, include: Include{file: tea}},
			},
		},
		{
			name: "self-include",
			graph: map[uri.URI][]Include{
				tea: {Include{file: tea}},
			},
			want: []CyclePair{
				{file: tea, include: Include{file: tea}},
			},
		},
		{
			name: "diamond into a cycle",
			graph: map[uri.URI][]Include{
				club: {Include{file: tea}, Include{file: git}},
				tea:  {Include{file: bass}},
				git:  {Include{file: drum}},
				bass: {Include{file: drum}, Include{file: club}},
				drum: {Include{file: tea}},
			},
			want: []CyclePair{
				{file: club, include: Include{file: tea}},
				{file: club, include: Include{file: git}},
				{file: tea, include: Include{file: bass}},
				{file: git, include: Include{file: drum}},
				{file: bass, include: Include{file: club}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cycleDetect(&tt.graph)
			sort.SliceStable(got, func(i, j int) bool {
				if got[i].file == got[j].file {
					return got[i].include.file < got[j].include.file
				}

				return got[i].file < got[j].file
			})

			assert.ElementsMatch(t, tt.want, got)
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

	// Expected includes, parsed through the same snapshot so the ParsedFile
	// pointers match the ones getIncludes stores.
	pfFor := func(uriStr string) *cache.ParsedFile {
		pf, err := ss.Parse(t.Context(), uri.URI(uriStr))
		require.NoError(t, err)

		return pf
	}
	userPf := pfFor("file:///tmp/user.thrift")
	goodsPf := pfFor("file:///tmp/test/goods.thrift")
	addressPf := pfFor("file:///tmp/test/address.thrift")

	expectIncludeMap := map[uri.URI][]Include{
		"file:///tmp/user.thrift": {
			Include{file: "file:///tmp/test/goods.thrift", include: userPf.AST().Includes()[0], pf: userPf},
			Include{file: "file:///tmp/test/address.thrift", include: userPf.AST().Includes()[1], pf: userPf},
		},
		"file:///tmp/test/goods.thrift": {
			Include{file: "file:///tmp/user.thrift", include: goodsPf.AST().Includes()[0], pf: goodsPf},
		},
		"file:///tmp/test/address.thrift": {
			Include{file: "file:///tmp/user.thrift", include: addressPf.AST().Includes()[0], pf: addressPf},
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
	c := cache.New(nil)
	fs := cache.NewOverlayFS(c)
	_ = fs.Update(t.Context(), files)

	view := cache.NewView("test", "file:///tmp", fs, nil)
	ss := cache.NewSnapshot(view, nil)

	return ss
}
