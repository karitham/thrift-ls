package analyzers

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/analyzertest"
	"github.com/karitham/thrift-ls/sema"
	"github.com/karitham/thrift-ls/store"
)

// cyclePair identifies one reported cycle include: the file containing the
// include statement and the resolved URI it points at. Diagnostics carry
// this as the message "cycle dependency in <to>".
type cyclePair struct {
	from uri.URI
	to   uri.URI
}

func sortedPairs(t *testing.T, res sema.Report) []cyclePair {
	t.Helper()

	pairs := make([]cyclePair, 0)
	for file, diags := range res {
		for _, d := range diags {
			pairs = append(pairs, cyclePair{
				from: file,
				to:   uri.URI(strings.TrimPrefix(d.Message, "cycle dependency in ")),
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

	res := analyzertest.Run(t, &CycleCheck{}, files, root)

	for file, diags := range res {
		for _, d := range diags {
			assert.Equal(t, sema.SeverityWarning, d.Severity, file)
			assert.Equal(t, sema.CodeIncludeCycle, d.Code, file)
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

	view := analyzertest.View(t, map[string]string{
		"user.thrift":         file1,
		"test/goods.thrift":   file2,
		"test/address.thrift": file3,
	})

	// Expected includes, parsed through the same snapshot so the ParsedFile
	// pointers match the ones getIncludes stores.
	pfFor := func(name string) *store.ParsedFile {
		pf, err := view.Parse(t.Context(), analyzertest.URI(name))
		require.NoError(t, err)

		return pf
	}
	userPf := pfFor("user.thrift")
	goodsPf := pfFor("test/goods.thrift")
	addressPf := pfFor("test/address.thrift")

	expectIncludeMap := map[uri.URI][]Include{
		analyzertest.URI("user.thrift"): {
			Include{file: analyzertest.URI("test/goods.thrift"), include: userPf.AST().Includes()[0], pf: userPf},
			Include{file: analyzertest.URI("test/address.thrift"), include: userPf.AST().Includes()[1], pf: userPf},
		},
		analyzertest.URI("test/goods.thrift"): {
			Include{file: analyzertest.URI("user.thrift"), include: goodsPf.AST().Includes()[0], pf: goodsPf},
		},
		analyzertest.URI("test/address.thrift"): {
			Include{file: analyzertest.URI("user.thrift"), include: addressPf.AST().Includes()[0], pf: addressPf},
		},
	}

	includeMap := make(map[uri.URI][]Include)

	err := getIncludes(t.Context(), view, analyzertest.URI("user.thrift"), &includeMap)
	require.NoError(t, err)

	assert.Equal(t, expectIncludeMap, includeMap)
}

func Test_getIncludes_Unresolvable(t *testing.T) {
	view := analyzertest.View(t, map[string]string{
		"user.thrift": "include \"ghost.thrift\"\nstruct S {}",
	})

	includeMap := make(map[uri.URI][]Include)

	err := getIncludes(t.Context(), view, analyzertest.URI("user.thrift"), &includeMap)
	require.NoError(t, err, "unresolvable includes are data, not errors")
	require.Contains(t, includeMap, analyzertest.URI("user.thrift"))
	assert.Empty(t, includeMap["file:///tmp/ghost.thrift"], "ghost target is never parsed into the map")
}
