package source

import (
	"context"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/formatter"
	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

// Hover returns the formatted definition under the cursor: a type
// reference, a constant value identifier, or a service reference.
func Hover(ctx context.Context, view *cache.View, file uri.URI, pos protocol.Position) (res string, err error) {
	pf, target, err := resolveTarget(ctx, view, file, pos)
	if err != nil {
		return res, err
	}

	ix := NewIndex(view)

	switch target.kind {
	case TargetTypeName:
		return hoverDefinition(ctx, ix, pf, target)
	case TargetConstValue:
		return hoverConstValue(ctx, ix, pf, target)
	case TargetService:
		return hoverService(ctx, ix, pf, target)
	}

	return res, err
}

// formatNode renders a definition node as hover text with default options.
func formatNode(doc *syntax.Document, node syntax.Node) (string, error) {
	return formatter.FormatNode(doc, node, formatter.DefaultOptions())
}

func hoverService(ctx context.Context, ix *Index, pf *cache.ParsedFile, target *target) (string, error) {
	def, err := ix.ResolveService(ctx, pf, target.identifier())
	if err != nil || def == nil {
		return "", err
	}

	svc, _ := def.Node.(*syntax.Service)
	if svc == nil {
		return "", nil
	}

	return formatNode(def.Parsed.AST(), svc)
}

func hoverDefinition(ctx context.Context, ix *Index, pf *cache.ParsedFile, target *target) (string, error) {
	ft := target.parent.(*syntax.FieldType)

	def, err := ix.ResolveType(ctx, pf, ft)
	if err != nil || def == nil {
		return "", err
	}

	if !definitionMatches(def.Node, def.Kind) {
		return "", nil
	}

	return formatNode(def.Parsed.AST(), def.Node)
}

func hoverConstValue(ctx context.Context, ix *Index, pf *cache.ParsedFile, target *target) (string, error) {
	def, err := ix.ResolveValue(ctx, pf, target.node.(*syntax.ConstValue))
	if err != nil || def == nil {
		return "", err
	}

	if def.Kind == DefinitionEnumValue {
		dst := enumOfValue(def.Parsed, def.Name.Text)
		if dst == nil {
			return "", nil
		}

		return formatNode(def.Parsed.AST(), dst)
	}

	if dstConst, ok := def.Node.(*syntax.Const); ok {
		return formatNode(def.Parsed.AST(), dstConst)
	}

	return "", nil
}
