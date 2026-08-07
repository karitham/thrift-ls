package codejump

import (
	"context"
	"log/slog"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/lsputils"
	"github.com/karitham/thrift-ls/syntax"
)

// Definition returns the locations of the definition under the cursor:
// a type reference, a constant value identifier, or a service reference.
func Definition(ctx context.Context, ss *cache.Snapshot, file uri.URI, pos protocol.Position) (res []protocol.Location, err error) {
	res = make([]protocol.Location, 0)

	pf, target, err := resolveTarget(ctx, ss, file, pos)
	if err != nil {
		return res, err
	}

	switch target.kind {
	case TargetTypeName:
		return typeNameDefinition(ctx, ss, file, pf, target)
	case TargetConstValue:
		return constValueDefinition(ctx, ss, file, pf, target)
	case TargetService:
		return serviceDefinition(ctx, ss, file, pf, target)
	}

	return res, err
}

// FindTypeDefinition resolves a type reference to its definition: an
// exception, struct, enum, union, or typedef, possibly in an included file.
func FindTypeDefinition(ctx context.Context, ss *cache.Snapshot, file uri.URI, ast *syntax.Document, ft *syntax.FieldType) (uri.URI, *syntax.Identifier, DefinitionKind, error) {
	name := typeReferenceName(ft)
	if name == "" || IsBasicType(name) {
		return "", nil, DefinitionNone, nil
	}

	_, identifier := lsputils.ParseIdent(file, ast.Includes(), name)
	for _, astFile := range definitionFiles(ctx, ss, file, ast, name) {
		dstAst, err := parseDefinitionFile(ctx, ss, astFile)
		if err != nil {
			return astFile, nil, DefinitionNone, err
		}

		if dstException := GetExceptionNode(dstAst, identifier); dstException != nil {
			return astFile, dstException.Name, DefinitionException, nil
		}

		if dstStruct := GetStructNode(dstAst, identifier); dstStruct != nil {
			return astFile, dstStruct.Name, DefinitionStruct, nil
		}

		if dstEnum := GetEnumNode(dstAst, identifier); dstEnum != nil {
			return astFile, dstEnum.Name, DefinitionEnum, nil
		}

		if dstUnion := GetUnionNode(dstAst, identifier); dstUnion != nil {
			return astFile, dstUnion.Name, DefinitionUnion, nil
		}

		if dstTypedef := GetTypedefNode(dstAst, identifier); dstTypedef != nil {
			return astFile, dstTypedef.Name, DefinitionTypedef, nil
		}
	}

	return file, nil, DefinitionNone, nil
}

// FindConstValueDefinition resolves a constant value identifier to its
// definition: an enum value or a const, possibly in an included file.
func FindConstValueDefinition(ctx context.Context, ss *cache.Snapshot, file uri.URI, ast *syntax.Document, value *syntax.ConstValue) (uri.URI, *syntax.Identifier, error) {
	if value == nil || value.Kind != syntax.ValueIdent {
		return "", nil, nil
	}

	name := value.Text
	if name == "true" || name == "false" {
		return "", nil, nil
	}

	_, identifier := lsputils.ParseIdent(file, ast.Includes(), name)
	for _, astFile := range definitionFiles(ctx, ss, file, ast, name) {
		dstAst, err := parseDefinitionFile(ctx, ss, astFile)
		if err != nil {
			return astFile, nil, err
		}

		if dstEnumValue := GetEnumValueIdentifierNode(dstAst, identifier); dstEnumValue != nil {
			return astFile, dstEnumValue, nil
		}

		if constIdentifier := GetConstIdentifierNode(dstAst, identifier); constIdentifier != nil {
			return astFile, constIdentifier, nil
		}
	}

	return file, nil, nil
}

// FindServiceDefinition resolves a service name or extends reference to the
// service definition.
func FindServiceDefinition(ctx context.Context, ss *cache.Snapshot, file uri.URI, ast *syntax.Document, ident *syntax.Identifier) (uri.URI, *syntax.Identifier, error) {
	if ident == nil {
		return "", nil, nil
	}

	_, identifier := lsputils.ParseIdent(file, ast.Includes(), ident.Text)
	for _, astFile := range definitionFiles(ctx, ss, file, ast, ident.Text) {
		dstAst, err := parseDefinitionFile(ctx, ss, astFile)
		if err != nil {
			return astFile, nil, err
		}

		if dstService := GetServiceNode(dstAst, identifier); dstService != nil {
			return astFile, dstService.Name, nil
		}
	}

	return file, nil, nil
}

// parseDefinitionFile parses the definition file, tolerating parse errors
// in the target file (the definitions may still be found in the partial
// AST).
func parseDefinitionFile(ctx context.Context, ss *cache.Snapshot, file uri.URI) (*syntax.Document, error) {
	pf, err := ss.Parse(ctx, file)
	if err != nil {
		return nil, err
	}

	if len(pf.Errors()) > 0 {
		slog.Error("parse error", "errs", pf.Errors())
	}

	if pf.AST() == nil {
		return nil, errNoAST
	}

	return pf.AST(), nil
}

func typeNameDefinition(ctx context.Context, ss *cache.Snapshot, file uri.URI, pf *cache.ParsedFile, target *target) ([]protocol.Location, error) {
	ft := target.parent.(*syntax.FieldType)

	astFile, id, _, err := FindTypeDefinition(ctx, ss, file, pf.AST(), ft)
	if err != nil {
		return nil, err
	}

	if id == nil {
		return nil, nil
	}

	loc, err := jumpInFile(ctx, ss, astFile, id)
	if err != nil {
		return nil, err
	}

	return []protocol.Location{loc}, nil
}

func constValueDefinition(ctx context.Context, ss *cache.Snapshot, file uri.URI, pf *cache.ParsedFile, target *target) ([]protocol.Location, error) {
	astFile, id, err := FindConstValueDefinition(ctx, ss, file, pf.AST(), target.node.(*syntax.ConstValue))
	if err != nil {
		return nil, err
	}

	if id == nil {
		return nil, nil
	}

	loc, err := jumpInFile(ctx, ss, astFile, id)
	if err != nil {
		return nil, err
	}

	return []protocol.Location{loc}, nil
}

func serviceDefinition(ctx context.Context, ss *cache.Snapshot, file uri.URI, pf *cache.ParsedFile, target *target) ([]protocol.Location, error) {
	astFile, id, err := FindServiceDefinition(ctx, ss, file, pf.AST(), target.identifier())
	if err != nil {
		return nil, err
	}

	if id == nil {
		return nil, nil
	}

	loc, err := jumpInFile(ctx, ss, astFile, id)
	if err != nil {
		return nil, err
	}

	return []protocol.Location{loc}, nil
}
