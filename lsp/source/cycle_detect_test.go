package source

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/options"
)

func buildSnapshotForTest(t *testing.T, files []*cache.FileChange) *cache.View {
	t.Helper()

	c := cache.New()
	fs := cache.NewOverlayFS(c)
	_ = fs.Update(t.Context(), files)

	view := cache.NewView("file:///tmp", fs, nil, options.Patch{})

	for _, f := range files {
		_, _ = view.Parse(t.Context(), f.URI)
	}

	return view
}

// cyclePair identifies one reported cycle include: the file containing the
// include statement and the resolved URI it points at. Diagnostics carry
// this as the message "cycle dependency in <to>".
type cyclePair struct {
	from uri.URI
	to   uri.URI
}

func sortedPairs(t *testing.T, res DiagnosticResult) []cyclePair {
	t.Helper()

	pairs := make([]cyclePair, 0)
	for file, diags := range res {
		for _, d := range diags {
			msg, ok := d.Message.(protocol.String)
			require.True(t, ok, "diagnostic message must be protocol.String")

			pairs = append(pairs, cyclePair{
				from: file,
				to:   uri.URI(strings.TrimPrefix(string(msg), "cycle dependency in ")),
			})
		}
	}

	slices.SortFunc(pairs, func(a, b cyclePair) int {
		if c := strings.Compare(string(a.from), string(b.from)); c != 0 {
			return c
		}

		return strings.Compare(string(a.to), string(b.to))
	})

	if len(pairs) == 0 {
		return nil
	}

	return pairs
}

// runCycleCheck builds a snapshot from an in-memory file tree rooted at
// file:///tmp and runs CycleCheck starting from root. It asserts every
// diagnostic carries the cycle code and warning severity, and returns the
// reported (from, to) pairs sorted.
func runCycleCheck(t *testing.T, files map[string]string, root string) []cyclePair {
	t.Helper()

	names := slices.Sorted(maps.Keys(files))

	changes := make([]*cache.FileChange, 0, len(files))
	for _, name := range names {
		changes = append(changes, &cache.FileChange{
			URI:     uri.URI("file:///tmp/" + name),
			Version: 0,
			Content: []byte(files[name]),
			From:    cache.FileChangeTypeDidOpen,
		})
	}

	view := buildSnapshotForTest(t, changes)

	res, err := (&CycleCheck{}).Diagnostic(t.Context(), view, []uri.URI{uri.URI("file:///tmp/" + root)})
	require.NoError(t, err)

	for file, diags := range res {
		for _, d := range diags {
			assert.Equal(t, protocol.DiagnosticSeverityWarning, d.Severity, file)
			assert.Equal(t, protocol.String(CodeIncludeCycle), d.Code, file)
		}
	}

	return sortedPairs(t, res)
}

// TestCycleCheck pins cycle detection end to end: an include edge X -> Y
// closes a cycle when Y transitively includes X back, no matter the cycle
// length. Every reported edge must point back along the cycle; acyclic
// graphs and unresolvable includes must produce nothing.
func TestCycleCheck(t *testing.T) {
	const (
		a    = "a.thrift"
		b    = "b.thrift"
		c    = "c.thrift"
		d    = "d.thrift"
		club = "clubroom.thrift"
		tea  = "tea_time.thrift"
		git  = "gitah.thrift"
		bass = "mio.thrift"
		drum = "ritsu.thrift"
	)

	tests := []struct {
		name  string
		files map[string]string
		root  string
		want  []cyclePair
	}{
		{
			name: "acyclic chain",
			files: map[string]string{
				a: `include "b.thrift"`,
				b: `include "c.thrift"`,
				c: ``,
			},
			root: a,
			want: nil,
		},
		{
			name: "two file cycle",
			files: map[string]string{
				a: `include "b.thrift"`,
				b: `include "a.thrift"`,
			},
			root: a,
			want: []cyclePair{
				{"file:///tmp/" + a, "file:///tmp/" + b},
				{"file:///tmp/" + b, "file:///tmp/" + a},
			},
		},
		{
			name: "three file cycle",
			files: map[string]string{
				a: `include "b.thrift"`,
				b: `include "c.thrift"`,
				c: `include "a.thrift"`,
			},
			root: a,
			want: []cyclePair{
				{"file:///tmp/" + a, "file:///tmp/" + b},
				{"file:///tmp/" + b, "file:///tmp/" + c},
				{"file:///tmp/" + c, "file:///tmp/" + a},
			},
		},
		{
			name: "four file cycle",
			files: map[string]string{
				a: `include "b.thrift"`,
				b: `include "c.thrift"`,
				c: `include "d.thrift"`,
				d: `include "a.thrift"`,
			},
			root: a,
			want: []cyclePair{
				{"file:///tmp/" + a, "file:///tmp/" + b},
				{"file:///tmp/" + b, "file:///tmp/" + c},
				{"file:///tmp/" + c, "file:///tmp/" + d},
				{"file:///tmp/" + d, "file:///tmp/" + a},
			},
		},
		{
			name: "self include",
			files: map[string]string{
				a: `include "a.thrift"`,
			},
			root: a,
			want: []cyclePair{
				{"file:///tmp/" + a, "file:///tmp/" + a},
			},
		},
		{
			name: "diamond into a cycle",
			files: map[string]string{
				club: "include \"" + tea + "\"\ninclude \"" + git + "\"",
				tea:  "include \"" + bass + "\"",
				git:  "include \"" + drum + "\"",
				bass: "include \"" + drum + "\"\ninclude \"" + club + "\"",
				drum: "include \"" + tea + "\"",
			},
			root: club,
			want: []cyclePair{
				{"file:///tmp/" + club, "file:///tmp/" + git},
				{"file:///tmp/" + club, "file:///tmp/" + tea},
				{"file:///tmp/" + git, "file:///tmp/" + drum},
				{"file:///tmp/" + bass, "file:///tmp/" + club},
				{"file:///tmp/" + bass, "file:///tmp/" + drum},
				{"file:///tmp/" + drum, "file:///tmp/" + tea},
				{"file:///tmp/" + tea, "file:///tmp/" + bass},
			},
		},
		{
			name: "unresolvable include is not a cycle",
			files: map[string]string{
				a: `include "ghost.thrift"`,
			},
			root: a,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runCycleCheck(t, tt.files, tt.root)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_getIncludes(t *testing.T) {
	file1 := `include "./test/goods.thrift"
include "./test/address.thrift"`
	file2 := `include "../user.thrift"`
	file3 := `include "../user.thrift"`

	view := buildSnapshotForTest(t, []*cache.FileChange{
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
		pf, err := view.Parse(t.Context(), uri.URI(uriStr))
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

	err := getIncludes(t.Context(), view, "file:///tmp/user.thrift", &includeMap)
	require.NoError(t, err)

	assert.Equal(t, expectIncludeMap, includeMap)
}
