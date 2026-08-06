package codejump

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
func Hover(ctx context.Context, ss *cache.Snapshot, file uri.URI, pos protocol.Position) (res string, err error) {
	pf, target, err := resolveTarget(ctx, ss, file, pos)
	if err != nil {
		return res, err
	}

	switch target.kind {
	case TargetTypeName:
		return hoverDefinition(ctx, ss, file, pf, target)
	case TargetConstValue:
		return hoverConstValue(ctx, ss, file, pf, target)
	case TargetService:
		return hoverService(ctx, ss, file, pf, target)
	}
	return res, err
}

// formatNode renders a definition node as hover text with default options.
func formatNode(doc *syntax.Document, node syntax.Node) (string, error) {
	return formatter.FormatNode(doc, node, formatter.DefaultOptions())
}

func hoverService(ctx context.Context, ss *cache.Snapshot, file uri.URI, pf *cache.ParsedFile, target *target) (string, error) {
	astFile, id, err := FindServiceDefinition(ctx, ss, file, pf.AST(), target.identifier())
	if err != nil || id == nil {
		return "", err
	}
	dstAst, err := parseDefinitionFile(ctx, ss, astFile)
	if err != nil {
		return "", err
	}
	svc := GetServiceNode(dstAst, id.Text)
	if svc == nil {
		return "", nil
	}
	return formatNode(dstAst, svc)
}

func hoverDefinition(ctx context.Context, ss *cache.Snapshot, file uri.URI, pf *cache.ParsedFile, target *target) (string, error) {
	ft := target.parent.(*syntax.FieldType)
	astFile, id, kind, err := FindTypeDefinition(ctx, ss, file, pf.AST(), ft)
	if err != nil || id == nil {
		return "", err
	}
	dstAst, err := parseDefinitionFile(ctx, ss, astFile)
	if err != nil {
		return "", err
	}

	var node syntax.Node
	switch kind {
	case DefinitionException:
		node = GetExceptionNode(dstAst, id.Text)
	case DefinitionStruct:
		node = GetStructNode(dstAst, id.Text)
	case DefinitionEnum:
		node = GetEnumNode(dstAst, id.Text)
	case DefinitionUnion:
		node = GetUnionNode(dstAst, id.Text)
	case DefinitionTypedef:
		node = GetTypedefNode(dstAst, id.Text)
	}
	if node == nil {
		return "", nil
	}
	return formatNode(dstAst, node)
}

func hoverConstValue(ctx context.Context, ss *cache.Snapshot, file uri.URI, pf *cache.ParsedFile, target *target) (string, error) {
	astFile, id, err := FindConstValueDefinition(ctx, ss, file, pf.AST(), target.node.(*syntax.ConstValue))
	if err != nil || id == nil {
		return "", err
	}
	dstAst, err := parseDefinitionFile(ctx, ss, astFile)
	if err != nil {
		return "", err
	}

	if dstEnum := GetEnumNodeByEnumValue(dstAst, id.Text); dstEnum != nil {
		return formatNode(dstAst, dstEnum)
	}
	if dstConst := GetConstNode(dstAst, id.Text); dstConst != nil {
		return formatNode(dstAst, dstConst)
	}
	return "", nil
}
