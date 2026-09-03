package source

import (
	"context"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/sema"
	"github.com/karitham/thrift-ls/store"
	"github.com/karitham/thrift-ls/syntax"
)

// TypeDefinition returns the definition of the type of the declaration
// under the cursor: for a type reference, the type's definition; for a
// field, function, typedef, or const name, the definition of its declared
// type.
func TypeDefinition(ctx context.Context, view *store.View, file uri.URI, pos protocol.Position) (res []protocol.Location, err error) {
	res = make([]protocol.Location, 0)

	pf, target, err := resolveTarget(ctx, view, file, pos)
	if err != nil {
		return res, err
	}

	switch target.kind {
	case TargetTypeName:
		return typeNameDefinition(ctx, sema.NewIndex(view), pf, target)
	case TargetConstValue:
		// The type definition of a constant value is the value's own
		// definition: the enum value or const it references.
		return constValueDefinition(ctx, sema.NewIndex(view), pf, target)
	case TargetDefinition:
		return declarationTypeDefinition(ctx, sema.NewIndex(view), pf, target)
	}

	return res, err
}

// declarationTypeDefinition jumps to the definition of the declared type of
// a field, typedef, function, or const under the cursor.
func declarationTypeDefinition(ctx context.Context, ix *sema.Index, pf *store.ParsedFile, target *target) ([]protocol.Location, error) {
	var ft *syntax.FieldType

	switch parent := target.parent.(type) {
	case *syntax.Field:
		ft = parent.Type
	case *syntax.Typedef:
		ft = parent.Type
	case *syntax.Function:
		ft = parent.Type
	case *syntax.Const:
		ft = parent.Type
	}

	if ft == nil {
		return nil, nil
	}

	def, err := ix.ResolveType(ctx, pf, ft)
	if err != nil || def == nil {
		return nil, err
	}

	loc, err := jumpInFile(ctx, ix.View(), def.File, def.Name)
	if err != nil {
		return nil, err
	}

	return []protocol.Location{loc}, nil
}
