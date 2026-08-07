package source

import (
	"context"
	"errors"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/types"
	"github.com/karitham/thrift-ls/syntax"
)

// TargetKind classifies what the cursor is on.
type TargetKind uint8

const (
	// TargetNone is a position with no relevant target.
	TargetNone TargetKind = iota
	// TargetTypeName is a type reference (a field/return/typedef type).
	TargetTypeName
	// TargetConstValue is an identifier in a constant value position.
	TargetConstValue
	// TargetService is a service name or an extends reference.
	TargetService
	// TargetDefinition is a definition name: struct, enum, const, typedef,
	// field, function, enum value, and so on.
	TargetDefinition
)

// target is the resolved cursor position: the node under the cursor, its
// kind, and the full node path from the document root.
type target struct {
	path   []syntax.Node
	node   syntax.Node
	parent syntax.Node
	kind   TargetKind
}

var errNoAST = errors.New("parse ast failed")

// resolveTarget parses the file and finds what the cursor is on.
func resolveTarget(ctx context.Context, ss *cache.Snapshot, file uri.URI, pos protocol.Position) (*cache.ParsedFile, *target, error) {
	pf, err := ss.Parse(ctx, file)
	if err != nil {
		return nil, nil, err
	}

	if pf.AST() == nil {
		return nil, nil, errNoAST
	}

	astPos, err := pf.Mapper().LSPPosToParserPosition(types.Position{Line: pos.Line, Character: pos.Character})
	if err != nil {
		return nil, nil, err
	}

	path := pf.AST().SearchNodePathByPosition(astPos)
	if len(path) == 0 {
		return nil, nil, errors.New("no node at position")
	}

	t := &target{
		path: path,
		node: path[len(path)-1],
	}
	if len(path) > 1 {
		t.parent = path[len(path)-2]
	}

	t.kind = classify(t)

	return pf, t, nil
}

// classify determines what the cursor is on from the deepest node and its
// parent. Identifiers carry the role of their parent: an identifier inside
// a FieldType is a type reference, an identifier inside a Service is a
// service name or extends, and any other identifier is a definition name.
func classify(t *target) TargetKind {
	switch n := t.node.(type) {
	case *syntax.Identifier:
		switch t.parent.(type) {
		case *syntax.FieldType:
			return TargetTypeName
		case *syntax.Service:
			return TargetService
		}

		return TargetDefinition
	case *syntax.ConstValue:
		if n.Kind == syntax.ValueIdent {
			return TargetConstValue
		}
	}

	return TargetNone
}

// targetIdentifier returns the identifier node for definition, service, and
// type-name targets.
func (t *target) identifier() *syntax.Identifier {
	if id, ok := t.node.(*syntax.Identifier); ok {
		return id
	}

	return nil
}

// jump builds an LSP location for a node.
func jump(file uri.URI, doc *syntax.Document, node syntax.Node) protocol.Location {
	return protocol.Location{
		Range: nodeRange(doc, node),
		URI:   file,
	}
}

// jumpInFile parses file and builds a location for a node belonging to that
// file's AST. Use this for nodes resolved from a different file than the
// one under the cursor: the node's token indices are only meaningful in its
// own document's token stream.
func jumpInFile(ctx context.Context, ss *cache.Snapshot, file uri.URI, node syntax.Node) (protocol.Location, error) {
	pf, err := ss.Parse(ctx, file)
	if err != nil {
		return protocol.Location{}, err
	}

	if pf.AST() == nil {
		return protocol.Location{}, errNoAST
	}

	return jump(file, pf.AST(), node), nil
}

// nodeRange converts a node span to an LSP range.
func nodeRange(doc *syntax.Document, node syntax.Node) protocol.Range {
	start, end := doc.Range(node)

	return protocol.Range{
		Start: protocol.Position{
			Line:      uint32(start.Line - 1),
			Character: uint32(start.Col - 1),
		},
		End: protocol.Position{
			Line:      uint32(end.Line - 1),
			Character: uint32(end.Col - 1),
		},
	}
}
