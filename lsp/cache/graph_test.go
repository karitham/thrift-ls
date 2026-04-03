package cache

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/joyme123/thrift-ls/parser"
	"github.com/stretchr/testify/assert"
	"go.lsp.dev/uri"
)

func Test_IncludeGraph_Set(t *testing.T) {
	graph := NewIncludeGraph()

	// Create temp directory structure:
	// /tmp/thrift-test/
	//   base/
	//     shared.thrift    (exists)
	//   service/
	//     order.thrift   (exists)

	tmpDir, err := os.MkdirTemp("", "thrift-test")
	assert.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	baseDir := filepath.Join(tmpDir, "base")
	serviceDir := filepath.Join(tmpDir, "service")
	err = os.MkdirAll(baseDir, 0755)
	assert.NoError(t, err)
	err = os.MkdirAll(serviceDir, 0755)
	assert.NoError(t, err)

	// Create shared.thrift in base/
	sharedThrift := filepath.Join(baseDir, "shared.thrift")
	err = os.WriteFile(sharedThrift, []byte(""), 0644)
	assert.NoError(t, err)

	// Create order.thrift in service/
	orderThrift := filepath.Join(serviceDir, "order.thrift")
	err = os.WriteFile(orderThrift, []byte(""), 0644)
	assert.NoError(t, err)

	orderURI := uri.File(orderThrift)
	sharedURI := uri.File(sharedThrift)

	// Test: include "shared.thrift" from order.thrift with includePaths pointing to base/
	// Should resolve to base/shared.thrift
	graph.Set(orderURI, []*parser.Include{
		{Path: &parser.Literal{Value: &parser.LiteralValue{Text: "shared.thrift"}}},
	}, []string{baseDir})

	node := graph.Get(orderURI)
	assert.NotNil(t, node)
	assert.Equal(t, []uri.URI{sharedURI}, node.OutDegree(), "should resolve to base/shared.thrift")

	// Test: include "shared.thrift" from order.thrift without includePaths
	// Should fall back to relative path (service/shared.thrift which doesn't exist)
	graph2 := NewIncludeGraph()
	graph2.Set(orderURI, []*parser.Include{
		{Path: &parser.Literal{Value: &parser.LiteralValue{Text: "shared.thrift"}}},
	}, []string{})

	node2 := graph2.Get(orderURI)
	assert.NotNil(t, node2)
	assert.Equal(t, []uri.URI{uri.File(filepath.Join(serviceDir, "shared.thrift"))}, node2.OutDegree(), "should fall back to relative path")
}

func Test_Graph(t *testing.T) {
	graph := NewIncludeGraph()

	// node1:
	//   file:///tmp/model/user.thrift
	//   include "../base.thrift"
	//   include "../addr.thrift"

	// node2:
	//   file:///tmp/base.thrift

	// node3:
	//   file:///tmp/addr.thrift
	//   include "./base.thrift"
	file1 := uri.New("file:///tmp/model/user.thrift")
	file2 := uri.New("file:///tmp/base.thrift")
	file3 := uri.New("file:///tmp/addr.thrift")
	graph.Set(file1, []*parser.Include{
		{Path: &parser.Literal{Value: &parser.LiteralValue{Text: "../base.thrift"}}},
		{Path: &parser.Literal{Value: &parser.LiteralValue{Text: "../addr.thrift"}}},
	}, nil)
	graph.Set(file2, nil, nil)
	graph.Set(file3, []*parser.Include{
		{Path: &parser.Literal{Value: &parser.LiteralValue{Text: "./base.thrift"}}},
	}, nil)

	expectNode1 := &IncludeNode{
		outdegree: []uri.URI{file3, file2},
	}
	expectNode2 := &IncludeNode{
		indegree: []uri.URI{file1, file3},
	}
	expectNode3 := &IncludeNode{
		indegree:  []uri.URI{file1},
		outdegree: []uri.URI{file2},
	}

	assert.Equal(t, expectNode1, graph.Get("file:///tmp/model/user.thrift"), "user.thrift")
	assert.Equal(t, expectNode2, graph.Get("file:///tmp/base.thrift"), "base.thrift")
	assert.Equal(t, expectNode3, graph.Get("file:///tmp/addr.thrift"), "addr.thrift")

	assert.Equal(t, expectNode1, expectNode1.Clone())
	assert.Equal(t, expectNode2, expectNode2.Clone())
	assert.Equal(t, expectNode3, expectNode3.Clone())

	graph.Remove(file2)
	assert.Equal(t, expectNode1, graph.Get("file:///tmp/model/user.thrift"), "user.thrift")
	assert.Equal(t, expectNode2, graph.Get("file:///tmp/base.thrift"), "base.thrift")
	assert.Equal(t, expectNode3, graph.Get("file:///tmp/addr.thrift"), "addr.thrift")

	graph.Remove(file1)
	expectNode1 = nil
	expectNode2 = &IncludeNode{
		indegree: []uri.URI{file3},
	}
	expectNode3 = &IncludeNode{
		outdegree: []uri.URI{file2},
	}
	assert.Equal(t, expectNode1, graph.Get("file:///tmp/model/user.thrift"), "user.thrift")
	assert.Equal(t, expectNode2, graph.Get("file:///tmp/base.thrift"), "base.thrift")
	assert.Equal(t, expectNode3, graph.Get("file:///tmp/addr.thrift"), "addr.thrift")

	graph.Remove(file3)
	assert.Nil(t, graph.Get("file:///tmp/model/user.thrift"), "user.thrift")
	assert.Nil(t, graph.Get("file:///tmp/base.thrift"), "base.thrift")
	assert.Nil(t, graph.Get("file:///tmp/addr.thrift"), "addr.thrift")
}
