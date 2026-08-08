package cache

import (
	"slices"
	"strings"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/syntax"
)

// IncludeDeps tracks, per file, its transitive include dependencies. It owns the
// IncludeGraph; callers never touch the underlying graph directly.
type IncludeDeps struct {
	graph *IncludeGraph
}

// NewIncludeDeps returns an empty dependency set.
func NewIncludeDeps() *IncludeDeps {
	return &IncludeDeps{graph: NewIncludeGraph()}
}

// Includes returns the files file includes directly, sorted ascending by URI.
func (c *IncludeDeps) Includes(file uri.URI) []uri.URI {
	node := c.graph.Get(file)
	if node == nil {
		return nil
	}

	return node.OutDegree()
}

// Includers returns the files that include file directly, in graph order.
func (c *IncludeDeps) Includers(file uri.URI) []uri.URI {
	node := c.graph.Get(file)
	if node == nil {
		return nil
	}

	return node.InDegree()
}

// Register replaces file's include edges, resolving them via resolve the same
// way snapshot parsing does. It returns the URIs whose dependency set
// changed: the union of file's old and new transitive dependents.
//
// Duplicate includes resolve once; unknown include paths fall back to a
// relative path (see resolver.Resolve) and never crash.
func (c *IncludeDeps) Register(file uri.URI, includes []*syntax.Include, resolve func(uri.URI, string) uri.URI) []uri.URI {
	oldDeps := c.Dependents(file)

	c.graph.Set(file, dedupeIncludes(includes), resolve)

	return unionDependents(oldDeps, c.Dependents(file))
}

// Dependents returns every file that directly or transitively includes file,
// including file itself when it transitively includes itself. The result is
// sorted ascending by URI and cycle-safe.
func (c *IncludeDeps) Dependents(file uri.URI) []uri.URI {
	deps := make([]uri.URI, 0)
	seen := make(map[uri.URI]struct{})

	var walk func(f uri.URI)

	walk = func(f uri.URI) {
		node := c.graph.Get(f)
		if node == nil {
			return
		}

		for _, dependent := range node.InDegree() {
			if _, ok := seen[dependent]; ok {
				continue
			}

			seen[dependent] = struct{}{}
			deps = append(deps, dependent)

			walk(dependent)
		}
	}

	walk(file)

	slices.Sort(deps)

	return deps
}

// Forget removes file's edges and returns its former dependents.
func (c *IncludeDeps) Forget(file uri.URI) []uri.URI {
	deps := c.Dependents(file)

	c.graph.Remove(file)

	return deps
}

// Clone returns a deep copy, for snapshot copy-on-write.
func (c *IncludeDeps) Clone() *IncludeDeps {
	return &IncludeDeps{graph: c.graph.Clone()}
}

// dedupeIncludes drops include statements with the same path text, keeping
// the first occurrence.
func dedupeIncludes(includes []*syntax.Include) []*syntax.Include {
	deduped := make([]*syntax.Include, 0, len(includes))
	seen := make(map[string]struct{})

	for _, inc := range includes {
		if inc.Path == nil {
			continue
		}

		text := strings.Trim(inc.Path.Text, "\"'")
		if _, ok := seen[text]; ok {
			continue
		}

		seen[text] = struct{}{}

		deduped = append(deduped, inc)
	}

	return deduped
}

// unionDependents merges two dependent sets, deduped and sorted ascending by
// URI.
func unionDependents(a, b []uri.URI) []uri.URI {
	union := make([]uri.URI, 0, len(a)+len(b))
	seen := make(map[uri.URI]struct{}, len(a)+len(b))

	for _, deps := range [][]uri.URI{a, b} {
		for _, dep := range deps {
			if _, ok := seen[dep]; ok {
				continue
			}

			seen[dep] = struct{}{}
			union = append(union, dep)
		}
	}

	slices.Sort(union)

	return union
}
