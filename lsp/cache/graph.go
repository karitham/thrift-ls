package cache

import (
	"sort"
	"strings"
	"sync"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/syntax"
)

type IncludeNode struct {
	indegree  []uri.URI // uri of nodes which include this node
	outdegree []uri.URI // includes
}

func (n *IncludeNode) Clone() *IncludeNode {
	newNode := &IncludeNode{}
	if len(n.indegree) > 0 {
		newNode.indegree = make([]uri.URI, len(n.indegree))
	}

	if len(n.outdegree) > 0 {
		newNode.outdegree = make([]uri.URI, len(n.outdegree))
	}

	copy(newNode.indegree, n.indegree)
	copy(newNode.outdegree, n.outdegree)

	return newNode
}

func (n *IncludeNode) InDegree() []uri.URI {
	return n.indegree
}

func (n *IncludeNode) OutDegree() []uri.URI {
	return n.outdegree
}

// IncludeGraph tracks include edges between files. Snapshots share the
// underlying mapper (Clone is O(1)); the first structural change after a
// clone deep-copies the graph, so edits that do not change include edges
// never pay for the copy.
type IncludeGraph struct {
	mu     sync.RWMutex
	mapper map[uri.URI]*IncludeNode
	shared bool
}

func NewIncludeGraph() *IncludeGraph {
	return &IncludeGraph{
		mapper: make(map[uri.URI]*IncludeNode),
	}
}

// Get returns a copy of file's node. The graph's nodes are mutated in place
// by Set and removeWithoutLock, so a live node must never escape the lock.
func (g *IncludeGraph) Get(file uri.URI) *IncludeNode {
	g.mu.RLock()
	defer g.mu.RUnlock()

	node := g.mapper[file]
	if node == nil {
		return nil
	}

	return node.Clone()
}

// Set records the include edges of file. resolve maps an include path text
// to the URI of the included file (e.g. the snapshot's include resolver).
func (g *IncludeGraph) Set(file uri.URI, includes []*syntax.Include, resolve func(cur uri.URI, includePath string) uri.URI) {
	g.mu.Lock()
	defer g.mu.Unlock()

	includeURIs := make([]uri.URI, 0, len(includes))
	for _, inc := range includes {
		if inc.Path == nil {
			continue
		}

		includeURI := resolve(file, strings.Trim(inc.Path.Text, "\"'"))
		includeURIs = append(includeURIs, includeURI)
	}

	sort.SliceStable(includeURIs, func(i, j int) bool {
		return includeURIs[i] < includeURIs[j]
	})

	// Unchanged edges: nothing to write, so a shared snapshot graph is
	// left untouched (no copy).
	if node, ok := g.mapper[file]; ok && sameOutdegree(node, includeURIs) {
		return
	}

	g.detach()
	g.removeWithoutLock(file)

	node := g.mapper[file]
	if node == nil {
		node = &IncludeNode{}
	}

	for _, inc := range includeURIs {
		node.outdegree = append(node.outdegree, inc)

		if inc == file {
			// Self-include: the target node is this node. Appending to a
			// fresh node would be overwritten by g.mapper[file] below and
			// the edge lost.
			node.indegree = append(node.indegree, file)

			continue
		}

		outNode, exist := g.mapper[inc]
		if !exist {
			outNode = &IncludeNode{}
			g.mapper[inc] = outNode
		}

		outNode.indegree = append(outNode.indegree, file)
	}

	g.mapper[file] = node
}

// sameOutdegree reports whether the node's outdegree equals includeURIs.
// The node's slice is not modified.
func sameOutdegree(node *IncludeNode, includeURIs []uri.URI) bool {
	if len(node.outdegree) != len(includeURIs) {
		return false
	}

	out := make([]uri.URI, len(node.outdegree))
	copy(out, node.outdegree)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i] < out[j]
	})

	for i := range includeURIs {
		if out[i] != includeURIs[i] {
			return false
		}
	}

	return true
}

func (g *IncludeGraph) Remove(file uri.URI) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.detach()
	g.removeWithoutLock(file)
}

// Clone returns a view sharing the same mapper. The clone and the original
// both become copy-on-write: the next structural change deep-copies.
func (g *IncludeGraph) Clone() *IncludeGraph {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.shared = true

	return &IncludeGraph{mapper: g.mapper, shared: true}
}

// detach deep-copies the mapper before the first structural change after a
// clone, so the shared parent snapshot is never mutated. Callers must hold
// mu.
func (g *IncludeGraph) detach() {
	if !g.shared {
		return
	}

	mapper := make(map[uri.URI]*IncludeNode, len(g.mapper)+1)
	for file, node := range g.mapper {
		mapper[file] = node.Clone()
	}

	g.mapper = mapper
	g.shared = false
}

func (g *IncludeGraph) removeWithoutLock(file uri.URI) {
	node, ok := g.mapper[file]
	if !ok {
		return
	}

	for _, outFile := range node.outdegree {
		outNode, exist := g.mapper[outFile]
		if !exist {
			continue
		}

		for i := range outNode.indegree {
			if outNode.indegree[i] == file {
				outNode.indegree = append(outNode.indegree[0:i], outNode.indegree[i+1:]...)
				if len(outNode.indegree) == 0 {
					outNode.indegree = nil
				}

				break
			}
		}

		if len(outNode.indegree) == 0 && len(outNode.outdegree) == 0 {
			delete(g.mapper, outFile)
		}
	}

	node.outdegree = nil

	if len(node.indegree) == 0 && len(node.outdegree) == 0 {
		delete(g.mapper, file)
	}
}
