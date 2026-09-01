package source

import (
	"context"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

// Definition returns the locations of the definition under the cursor:
// a type reference, a constant value identifier, or a service reference.
func Definition(ctx context.Context, view *cache.View, file uri.URI, pos protocol.Position) (res []protocol.Location, err error) {
	res = make([]protocol.Location, 0)

	pf, target, err := resolveTarget(ctx, view, file, pos)
	if err != nil {
		return res, err
	}

	switch target.kind {
	case TargetTypeName:
		return typeNameDefinition(ctx, NewIndex(view), pf, target)
	case TargetConstValue:
		return constValueDefinition(ctx, NewIndex(view), pf, target)
	case TargetService:
		return serviceDefinition(ctx, NewIndex(view), pf, target)
	}

	return res, err
}

func typeNameDefinition(ctx context.Context, ix *Index, pf *cache.ParsedFile, target *target) ([]protocol.Location, error) {
	ft := target.fieldType()
	if ft == nil {
		return nil, nil
	}

	def, err := ix.ResolveType(ctx, pf, ft)
	if err != nil || def == nil {
		return nil, err
	}

	loc, err := jumpInFile(ctx, ix.view, def.File, def.Name)
	if err != nil {
		return nil, err
	}

	return []protocol.Location{loc}, nil
}

func constValueDefinition(ctx context.Context, ix *Index, pf *cache.ParsedFile, target *target) ([]protocol.Location, error) {
	def, err := ix.ResolveValue(ctx, pf, target.node.(*syntax.ConstValue))
	if err != nil || def == nil {
		return nil, err
	}

	loc, err := jumpInFile(ctx, ix.view, def.File, def.Name)
	if err != nil {
		return nil, err
	}

	return []protocol.Location{loc}, nil
}

func serviceDefinition(ctx context.Context, ix *Index, pf *cache.ParsedFile, target *target) ([]protocol.Location, error) {
	def, err := ix.ResolveService(ctx, pf, target.identifier())
	if err != nil || def == nil {
		return nil, err
	}

	loc, err := jumpInFile(ctx, ix.view, def.File, def.Name)
	if err != nil {
		return nil, err
	}

	return []protocol.Location{loc}, nil
}
