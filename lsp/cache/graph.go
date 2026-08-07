package cache

import (
	"log/slog"
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

type IncludeGraph struct {
	mu     sync.RWMutex
	mapper map[uri.URI]*IncludeNode
}

func NewIncludeGraph() *IncludeGraph {
	return &IncludeGraph{
		mapper: make(map[uri.URI]*IncludeNode),
	}
}

func (g *IncludeGraph) Get(file uri.URI) *IncludeNode {
	g.mu.RLock()
	defer g.mu.RUnlock()

	return g.mapper[file]
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

	node, ok := g.mapper[file]
	if ok {
		if len(includeURIs) == len(node.outdegree) {
			sort.SliceStable(node.outdegree, func(i, j int) bool {
				return node.outdegree[i] < node.outdegree[j]
			})

			equal := true

			for i := range includeURIs {
				if includeURIs[i] != node.outdegree[i] {
					equal = false

					break
				}
			}

			if equal {
				return
			}
		}

		g.removeWithoutLock(file)
	} else {
		node = &IncludeNode{}
	}

	for _, inc := range includeURIs {
		node.outdegree = append(node.outdegree, inc)

		outNode, exist := g.mapper[inc]
		if !exist {
			outNode = &IncludeNode{}
			g.mapper[inc] = outNode
		}

		outNode.indegree = append(outNode.indegree, file)
	}

	g.mapper[file] = node
}

func (g *IncludeGraph) Remove(file uri.URI) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.removeWithoutLock(file)
}

func (g *IncludeGraph) Clone() *IncludeGraph {
	g.mu.RLock()
	defer g.mu.RUnlock()

	newG := NewIncludeGraph()
	for i := range g.mapper {
		newG.mapper[i] = g.mapper[i].Clone()
	}

	return newG
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

		// update outNode indegree
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

func (g *IncludeGraph) Debug() {
	for file, node := range g.mapper {
		slog.Debug("graph file node", "file", file, "node", node)
	}
}
