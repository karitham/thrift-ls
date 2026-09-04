package analyzers

import (
	"context"
	"fmt"

	"github.com/karitham/thrift-ls/sema"
	"github.com/karitham/thrift-ls/store"
	"github.com/karitham/thrift-ls/syntax"
)

// NonScalarMapKeyCheck reports map key types that are not scalar or enum
// types. Structs, unions, exceptions, and containers are not valid map keys
// in standard Thrift, while typedefs are followed to their underlying type.
type NonScalarMapKeyCheck struct{}

// NonScalarMapKeyCheckName is the configuration name of
// NonScalarMapKeyCheck.
const NonScalarMapKeyCheckName = "NonScalarMapKeyCheck"

func (c *NonScalarMapKeyCheck) Name() string {
	return NonScalarMapKeyCheckName
}

func (c *NonScalarMapKeyCheck) AnalyzeFile(ctx context.Context, f sema.File) ([]sema.Diagnostic, error) {
	var res []sema.Diagnostic

	syntax.Walk(f.PF.AST(), func(n syntax.Node) bool {
		v, ok := n.(*syntax.FieldType)
		if !ok || v.Kind != syntax.TypeMap || v.KeyType == nil {
			return true
		}

		if d := c.checkMapKeyScalar(ctx, f.View(), f.Index(), f.PF, v.KeyType); d != nil {
			res = append(res, *d)
		}

		return true
	})

	return res, nil
}

func (c *NonScalarMapKeyCheck) checkMapKeyScalar(ctx context.Context, view sema.Graph, ix *sema.Index, pf *store.ParsedFile, key *syntax.FieldType) *sema.Diagnostic {
	kind := c.mapKeyKind(ctx, view, ix, pf, key, 0)
	if kind == "" {
		return nil
	}

	return &sema.Diagnostic{
		Span:     sema.SpanOf(pf, key),
		Severity: sema.SeverityWarning,
		Code:     sema.CodeNonScalarMapKey,
		Message:  fmt.Sprintf("map key must be a scalar type, found %s", kind),
	}
}

// mapKeyKind reports why key is not a scalar map key: the container kind,
// or the definition kind for struct-like types. "" means scalar: a base
// type, an enum, or a typedef chain ending there.
func (c *NonScalarMapKeyCheck) mapKeyKind(ctx context.Context, view sema.Graph, ix *sema.Index, pf *store.ParsedFile, key *syntax.FieldType, depth int) string {
	if key == nil {
		return ""
	}

	switch key.Kind {
	case syntax.TypeBase:
		return ""
	case syntax.TypeMap:
		return "map"
	case syntax.TypeList:
		return "list"
	case syntax.TypeSet:
		return "set"
	case syntax.TypeIdent:
		name := sema.TypeReferenceName(key)
		if name == "" || sema.IsBasicType(name) || depth > 8 {
			return ""
		}

		def, err := ix.ResolveType(ctx, pf, key)
		if err != nil || def == nil {
			return ""
		}

		switch def.Kind {
		case sema.DefinitionEnum:
			return ""
		case sema.DefinitionStruct, sema.DefinitionUnion, sema.DefinitionException:
			return kindLabel(def.Kind)
		case sema.DefinitionTypedef:
			td, ok := def.Node.(*syntax.Typedef)
			if !ok {
				return ""
			}

			return c.mapKeyKind(ctx, view, ix, def.Parsed, td.Type, depth+1)
		}
	}

	return ""
}

// kindLabel is the message label of a definition kind.
func kindLabel(k sema.DefinitionKind) string {
	switch k {
	case sema.DefinitionStruct:
		return "struct"
	case sema.DefinitionUnion:
		return "union"
	case sema.DefinitionException:
		return "exception"
	}

	return "type"
}
