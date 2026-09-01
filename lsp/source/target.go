package source

import (
	"context"
	"errors"
	"unicode/utf8"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
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
func resolveTarget(ctx context.Context, view *cache.View, file uri.URI, pos protocol.Position) (*cache.ParsedFile, *target, error) {
	pf, err := view.Parse(ctx, file)
	if err != nil {
		return nil, nil, err
	}

	if pf.AST() == nil {
		return nil, nil, errNoAST
	}

	astPos, err := pf.Mapper().LSPPosToParserPosition(protocol.Position{Line: pos.Line, Character: pos.Character})
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
// a FieldType or a structured annotation is a type reference, an
// identifier inside a Service is a service name or extends, and any other
// identifier is a definition name.
func classify(t *target) TargetKind {
	switch n := t.node.(type) {
	case *syntax.Identifier:
		switch t.parent.(type) {
		case *syntax.FieldType, *syntax.StructuredAnnotation:
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

// fieldType returns the type reference a type-name target points at: the
// FieldType parent, or — for a structured annotation's name — a synthetic
// FieldType built from the annotation, so annotation names resolve through
// the same path as any other type reference. nil for a non-type target.
func (t *target) fieldType() *syntax.FieldType {
	if ft, ok := t.parent.(*syntax.FieldType); ok {
		return ft
	}

	if sa, ok := t.parent.(*syntax.StructuredAnnotation); ok && sa.Name != nil {
		return &syntax.FieldType{Kind: syntax.TypeIdent, Ident: sa.Name}
	}

	return nil
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
func jump(file uri.URI, pf *cache.ParsedFile, node syntax.Node) protocol.Location {
	return protocol.Location{
		Range: nodeRange(pf, node),
		URI:   file,
	}
}

// jumpInFile parses file and builds a location for a node belonging to that
// file's AST. Use this for nodes resolved from a different file than the
// one under the cursor: the node's token indices are only meaningful in its
// own document's token stream.
func jumpInFile(ctx context.Context, view *cache.View, file uri.URI, node syntax.Node) (protocol.Location, error) {
	pf, err := view.Parse(ctx, file)
	if err != nil {
		return protocol.Location{}, err
	}

	if pf.AST() == nil {
		return protocol.Location{}, errNoAST
	}

	return jump(file, pf, node), nil
}

// toLSPPosition converts a parser position to a protocol position via the
// file mapper, so character columns are UTF-16 code units as the protocol
// requires. When the offset does not map (defensive), the rune column is
// the fallback.
func toLSPPosition(pf *cache.ParsedFile, pos syntax.Position) protocol.Position {
	p, err := pf.Mapper().OffsetToLSPPosition(pos.Offset)
	if err != nil {
		return protocol.Position{Line: uint32(pos.Line - 1), Character: uint32(pos.Col - 1)}
	}

	return p
}

// toLSPRange converts a parser span to an LSP range with UTF-16 columns.
func toLSPRange(pf *cache.ParsedFile, start, end syntax.Position) protocol.Range {
	return protocol.Range{Start: toLSPPosition(pf, start), End: toLSPPosition(pf, end)}
}

// nodeRange converts a node span to an LSP range.
func nodeRange(pf *cache.ParsedFile, node syntax.Node) protocol.Range {
	start, end := pf.AST().Range(node)

	return toLSPRange(pf, start, end)
}

// tokenRange converts a token's span to an LSP range.
func tokenRange(pf *cache.ParsedFile, tok *syntax.Token) protocol.Range {
	if tok == nil {
		return protocol.Range{}
	}

	start := syntax.Position{Line: tok.Line, Col: tok.Col, Offset: tok.Offset}
	end := syntax.Position{Line: tok.Line, Col: tok.Col + utf8.RuneCountInString(tok.Text), Offset: tok.Offset + len(tok.Text)}

	return toLSPRange(pf, start, end)
}
