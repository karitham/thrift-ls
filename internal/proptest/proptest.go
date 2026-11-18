// Package proptest holds the deterministic generators and helpers shared by
// the fuzz-based test suites.
//
// A fuzz target registers a Corpus and draws from the stream derived from
// each input:
//
//	func FuzzX(f *testing.F) {
//		proptest.Fuzz(f, seeds, func(t *proptest.T) {
//			_ = proptest.IntRange(1, 10).Draw(t)
//		})
//	}
package proptest

import (
	"fmt"
	"hash/fnv"
	"math/rand"
	"slices"
	"testing"
	"unicode/utf8"

	"github.com/karitham/thrift-ls/formatter"
	"github.com/karitham/thrift-ls/options"
)

// T is one deterministic stream derived from a fuzz input. It embeds the
// test handle so generator helpers and assertions share one variable.
type T struct {
	*testing.T
	r *rand.Rand
}

// New derives a generator from seed. The same seed always yields the same
// stream, so a failing fuzz input replays exactly.
func New(t *testing.T, seed []byte) *T {
	h := fnv.New64a()
	_, _ = h.Write(seed)

	return &T{T: t, r: rand.New(rand.NewSource(int64(h.Sum64())))}
}

// Gen draws a value from a stream.
type Gen[V any] func(*T) V

// Draw produces the next value.
func (g Gen[V]) Draw(t *T) V { return g(t) }

// IntRange draws an integer in [lo, hi].
func (t *T) IntRange(lo, hi int) int {
	if hi <= lo {
		return lo
	}

	return lo + t.r.Intn(hi-lo+1)
}

// Bool draws a boolean.
func (t *T) Bool() bool { return t.r.Intn(2) == 0 }

// Int64 draws a non-negative integer.
func (t *T) Int64() int64 { return t.r.Int63() }

// Custom adapts a draw function to a generator.
func Custom[V any](fn func(*T) V) Gen[V] { return fn }

// IntRange draws integers in [lo, hi].
func IntRange(lo, hi int) Gen[int] {
	return func(t *T) int { return t.IntRange(lo, hi) }
}

// Bool draws booleans.
func Bool() Gen[bool] { return (*T).Bool }

// Int64 draws non-negative integers.
func Int64() Gen[int64] { return (*T).Int64 }

// SampledFrom draws one element of xs. The zero value is drawn for an empty
// slice.
func SampledFrom[S ~[]E, E any](xs S) Gen[E] {
	return func(t *T) E {
		if len(xs) == 0 {
			var zero E
			return zero
		}

		return xs[t.IntRange(0, len(xs)-1)]
	}
}

// SliceOfN draws a slice with length in [minLen, maxLen].
func SliceOfN[E any](elem Gen[E], minLen, maxLen int) Gen[[]E] {
	return func(t *T) []E {
		n := t.IntRange(minLen, maxLen)
		out := make([]E, 0, n)

		for range n {
			out = append(out, elem.Draw(t))
		}

		return out
	}
}

// SliceOfNDistinct draws a slice with unique keys.
func SliceOfNDistinct[E any, K comparable](elem Gen[E], minLen, maxLen int, keyFn func(E) K) Gen[[]E] {
	return func(t *T) []E {
		n := t.IntRange(minLen, maxLen)
		seen := make(map[K]struct{}, n)
		out := make([]E, 0, n)

		// Redraw collisions until the slice reaches n entries. The cap
		// keeps an input with too few distinct keys from looping forever.
		for attempts := 0; len(out) < n && attempts < n*20+100; attempts++ {
			v := elem.Draw(t)
			k := keyFn(v)
			if _, ok := seen[k]; ok {
				continue
			}

			seen[k] = struct{}{}
			out = append(out, v)
		}

		return out
	}
}

// MapOfN draws a map with size in [minLen, maxLen].
func MapOfN[K comparable, V any](key Gen[K], val Gen[V], minLen, maxLen int) Gen[map[K]V] {
	return func(t *T) map[K]V {
		n := t.IntRange(minLen, maxLen)
		out := make(map[K]V, n)

		for range n {
			out[key.Draw(t)] = val.Draw(t)
		}

		return out
	}
}

// Map transforms a generator's output.
func Map[U, V any](g Gen[U], fn func(U) V) Gen[V] {
	return func(t *T) V { return fn(g.Draw(t)) }
}

// Ptr draws a value and returns its address, optionally drawing nil.
func Ptr[E any](elem Gen[E], allowNil bool) Gen[*E] {
	return func(t *T) *E {
		if allowNil && t.Bool() {
			return nil
		}

		v := elem.Draw(t)

		return &v
	}
}

// Permutation draws a shuffled copy of xs.
func Permutation[S ~[]E, E any](xs S) Gen[S] {
	return func(t *T) S {
		out := slices.Clone(xs)
		t.r.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })

		return out
	}
}

// Corpus is a set of raw fuzz inputs.
type Corpus [][]byte

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
func (c Corpus) Run(t *testing.T, body func(*T)) {
	t.Helper()

	for i, seed := range c {
		t.Run(fmt.Sprintf("seed%d", i), func(t *testing.T) {
			body(New(t, seed))
		})
	}
}

// Fuzz registers the corpus and runs the target for every fuzz input.
func Fuzz(f *testing.F, seeds Corpus, body func(*T)) {
	seeds.Add(f)
	f.Fuzz(func(tb *testing.T, seed []byte) {
		body(New(tb, seed))
	})
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
