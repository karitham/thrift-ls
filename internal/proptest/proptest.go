// Package proptest holds helpers shared by the fuzz-based test suites.
package proptest

import (
	"fmt"
	"testing"
	"unicode/utf8"

	"github.com/karitham/thrift-ls/formatter"
	"github.com/karitham/thrift-ls/internal/fuzzgen"
	"github.com/karitham/thrift-ls/options"
)

// Corpus is a set of raw fuzz inputs.
type Corpus [][]byte

// Strings builds a corpus from text seeds.
func Strings(seeds ...string) Corpus {
	out := make(Corpus, len(seeds))
	for i, seed := range seeds {
		out[i] = []byte(seed)
	}

	return out
}

// Raw builds a corpus from byte seeds.
func Raw(seeds ...[]byte) Corpus { return Corpus(seeds) }

// Range builds a corpus from an index function.
func Range(count int, seed func(int) string) Corpus {
	out := make(Corpus, count)
	for i := range count {
		out[i] = []byte(seed(i))
	}

	return out
}

// Add registers the corpus with a fuzz target.
func (c Corpus) Add(f *testing.F) {
	for _, seed := range c {
		f.Add(seed)
	}
}

// Run replays the corpus as deterministic subtests.
func (c Corpus) Run(t *testing.T, body func(*fuzzgen.T)) {
	t.Helper()

	for i, seed := range c {
		t.Run(fmt.Sprintf("seed%d", i), func(t *testing.T) {
			body(fuzzgen.New(t, seed))
		})
	}
}

// Fuzz registers the corpus and runs the target for every fuzz input.
func Fuzz(f *testing.F, seeds Corpus, body func(*testing.T, []byte)) {
	seeds.Add(f)
	f.Fuzz(body)
}

// RuneBoundaries returns the byte offsets of rune starts plus the end of the
// content.
func RuneBoundaries(content string) []int {
	out := []int{0}
	off := 0

	for _, r := range content {
		off += utf8.RuneLen(r)
		out = append(out, off)
	}

	return out
}

// ModelLayered folds patches from lowest to highest precedence, taking each
// field from the highest layer that sets it.
func ModelLayered(lowest options.Patch, layers ...options.Patch) options.Patch {
	all := append([]options.Patch{lowest}, layers...)

	var out options.Patch

	for i := len(all) - 1; i >= 0; i-- {
		patch := all[i]

		if out.PrintWidth == nil {
			out.PrintWidth = patch.PrintWidth
		}

		if out.TabWidth == nil {
			out.TabWidth = patch.TabWidth
		}

		if out.Align == nil {
			out.Align = patch.Align
		}

		if out.Indent == nil {
			out.Indent = patch.Indent
		}

		if out.IncludePaths == nil {
			out.IncludePaths = patch.IncludePaths
		}

		if out.LogLevel == nil {
			out.LogLevel = patch.LogLevel
		}

		if out.Lint == nil {
			out.Lint = patch.Lint
		}
	}

	out.Separators = modelPerConstruct(all, func(p options.Patch) *formatter.Separators { return p.Separators })
	out.Break = modelPerConstruct(all, func(p options.Patch) *formatter.Break { return p.Break })

	return out
}

func modelPerConstruct[T comparable](all []options.Patch, get func(options.Patch) *formatter.PerConstruct[T]) *formatter.PerConstruct[T] {
	var out *formatter.PerConstruct[T]

	for i := len(all) - 1; i >= 0; i-- {
		src := get(all[i])
		if src == nil {
			continue
		}

		if out == nil {
			out = &formatter.PerConstruct[T]{}
		}

		for _, construct := range formatter.AllConstructs {
			var unset T
			if out.Get(construct) != unset {
				continue
			}

			if value := src.Get(construct); value != unset {
				out.Set(construct, value)
			}
		}
	}

	return out
}
