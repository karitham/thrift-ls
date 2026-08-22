package source

import (
	"context"
	"fmt"
	"log/slog"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

type SemanticAnalysis struct{}

func (s *SemanticAnalysis) Diagnostic(ctx context.Context, b *Batch, changeFiles []uri.URI) (DiagnosticResult, error) {

	res := make(DiagnosticResult)

	for _, file := range changeFiles {
		items, err := s.diagnostic(ctx, b, file)
		if err != nil {
			return nil, err
		}

		res[file] = items
	}

	return res, nil
}

func (s *SemanticAnalysis) Name() string {
	return "SemanticAnalysis"
}

func (s *SemanticAnalysis) diagnostic(ctx context.Context, b *Batch, changeFile uri.URI) ([]protocol.Diagnostic, error) {
	pf, err := b.Tree(ctx, changeFile)
	if err != nil {
		return nil, err
	}

	if pf.AST() == nil {
		// The file does not parse; the Parse checker reports that.
		slog.Debug("semantic analysis skipped: file does not parse", "file", changeFile)

		return nil, nil
	}

	for _, err := range pf.Errors() {
		slog.Debug("parse failed", "err", err)
	}

	// The run's shared index: resolutions are memoized per (file, name),
	// across every checker in the batch.
	res := s.checkDefinitionExist(ctx, b.View(), b.Index(), pf)

	return res, nil
}

// checkDefinitionExist reports undefined references in one document walk:
// type references that resolve to no definition (fields, signatures,
// consts, typedefs, nested container elements), constant-value identifiers
// resolving to no enum value or const at any nesting depth, map keys that
// are not scalar, and field defaults whose kind mismatches the field type.
func (s *SemanticAnalysis) checkDefinitionExist(ctx context.Context, view *cache.View, ix *Index, pf *cache.ParsedFile) []protocol.Diagnostic {
	res := make([]protocol.Diagnostic, 0)

	syntax.Walk(pf.AST(), func(n syntax.Node) bool {
		switch v := n.(type) {
		case *syntax.FieldType:
			res = append(res, s.checkTypeExist(ctx, view, ix, pf, v)...)

			if v.Kind == syntax.TypeMap && v.KeyType != nil {
				if dig := s.checkMapKeyScalar(ctx, view, ix, pf, v.KeyType); dig != nil {
					res = append(res, *dig)
				}
			}
		case *syntax.Field:
			if v.Value != nil {
				if dig := s.checkConstValueMatchType(pf, v); dig != nil {
					res = append(res, *dig)
				}
			}
		case *syntax.ConstValue:
			res = append(res, s.checkConstValueExist(ctx, view, ix, pf, v)...)
		}

		return true
	})

	return res
}

func (s *SemanticAnalysis) checkConstValueExist(ctx context.Context, view *cache.View, ix *Index,
	pf *cache.ParsedFile, cst *syntax.ConstValue,
) (res []protocol.Diagnostic) {
	if cst == nil || cst.Kind != syntax.ValueIdent {
		return res
	}

	if cst.Text == "true" || cst.Text == "false" {
		return res
	}

	def, err := ix.ResolveValue(ctx, pf, cst)
	if err != nil || def == nil {
		res = append(res, protocol.Diagnostic{
			Range:    nodeRange(pf, cst),
			Severity: protocol.DiagnosticSeverityError,
			Code:     protocol.String(CodeUndefinedValue),
			Source:   protocol.NewOptional("thrift-ls"),
			Message:  protocol.String("default value doesn't exist"),
		})
	}

	return res
}

func (s *SemanticAnalysis) checkConstValueMatchType(pf *cache.ParsedFile, field *syntax.Field) (res *protocol.Diagnostic) {
	if field.Value == nil {
		return nil
	}

	expect := typeName(field.Type)
	value := field.Value
	valueKind := value.Kind

	switch valueKind {
	case syntax.ValueList, syntax.ValueMap, syntax.ValueString, syntax.ValueDouble:
		if !sameKind(expect, valueKind) {
			return mismatchDiagnostic(pf, field, expect, kindName(valueKind))
		}
	case syntax.ValueInt:
		// true/false lex as int constants but are bools.
		if value.Text == "true" || value.Text == "false" {
			if expect != "bool" {
				return mismatchDiagnostic(pf, field, expect, "bool")
			}

			return nil
		}

		switch expect {
		case "i8", "i16", "i32", "i64":
		default:
			return mismatchDiagnostic(pf, field, expect, "i64")
		}
	case syntax.ValueIdent:
		if expect == "bool" {
			return mismatchDiagnostic(pf, field, expect, "identifier")
		}
	}

	return nil
}

func sameKind(expect string, kind syntax.ConstValueKind) bool {
	switch kind {
	case syntax.ValueList:
		return expect == "list"
	case syntax.ValueMap:
		return expect == "map"
	case syntax.ValueString:
		return expect == "string"
	case syntax.ValueDouble:
		return expect == "double"
	}

	return false
}

func kindName(kind syntax.ConstValueKind) string {
	switch kind {
	case syntax.ValueList:
		return "list"
	case syntax.ValueMap:
		return "map"
	case syntax.ValueString:
		return "string"
	case syntax.ValueDouble:
		return "double"
	case syntax.ValueInt:
		return "int"
	case syntax.ValueIdent:
		return "identifier"
	}

	return "unknown"
}

func mismatchDiagnostic(pf *cache.ParsedFile, field *syntax.Field, expect, got string) *protocol.Diagnostic {
	return &protocol.Diagnostic{
		Range:    nodeRange(pf, field.Value),
		Severity: protocol.DiagnosticSeverityError,
		Code:     protocol.String(CodeValueTypeMismatch),
		Source:   protocol.NewOptional("thrift-ls"),
		Message:  protocol.String(fmt.Sprintf("expect %s but got %s", expect, got)),
	}
}

// typeName returns the referenced type name of a field type, or the
// container/base keyword.
func typeName(ft *syntax.FieldType) string {
	if ft == nil {
		return ""
	}

	switch ft.Kind {
	case syntax.TypeIdent:
		return ft.Ident.Text
	case syntax.TypeMap:
		return "map"
	case syntax.TypeList:
		return "list"
	case syntax.TypeSet:
		return "set"
	case syntax.TypeBase:
		return ft.Base.String()
	}

	return ""
}

// checkTypeExist reports a single type reference that resolves to no
// definition. The walk visits every FieldType individually — including
// container element types — so this needs no recursion.
func (s *SemanticAnalysis) checkTypeExist(ctx context.Context, view *cache.View, ix *Index,
	pf *cache.ParsedFile, ft *syntax.FieldType,
) (res []protocol.Diagnostic) {
	if ft == nil || ft.Kind != syntax.TypeIdent {
		return res
	}

	def, err := ix.ResolveType(ctx, pf, ft)
	if err == nil && def != nil {
		return res
	}

	return append(res, protocol.Diagnostic{
		Range:    nodeRange(pf, ft.Ident),
		Severity: protocol.DiagnosticSeverityError,
		Code:     protocol.String(CodeUndefinedType),
		Source:   protocol.NewOptional("thrift-ls"),
		Message:  protocol.String("field type doesn't exist"),
	})
}

// checkMapKeyScalar returns an error when the map key type is not scalar:
// thrift requires map keys to be a base type or an enum. Structs, unions,
// exceptions, and containers cannot be keys; typedefs are followed.
func (s *SemanticAnalysis) checkMapKeyScalar(ctx context.Context, view *cache.View, ix *Index, pf *cache.ParsedFile, key *syntax.FieldType) *protocol.Diagnostic {
	kind := s.mapKeyKind(ctx, view, ix, pf, key, 0)
	if kind == "" {
		return nil
	}

	return &protocol.Diagnostic{
		Range:    nodeRange(pf, key),
		Severity: protocol.DiagnosticSeverityError,
		Code:     protocol.String(CodeNonScalarMapKey),
		Source:   protocol.NewOptional("thrift-ls"),
		Message:  protocol.String(fmt.Sprintf("map key must be a scalar type, found %s", kind)),
	}
}

// mapKeyKind reports why key is not a scalar map key: the container kind,
// or the definition kind for struct-like types. "" means scalar: a base
// type, an enum, or a typedef chain ending there.
func (s *SemanticAnalysis) mapKeyKind(ctx context.Context, view *cache.View, ix *Index, pf *cache.ParsedFile, key *syntax.FieldType, depth int) string {
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
		name := typeReferenceName(key)
		if name == "" || IsBasicType(name) || depth > 8 {
			return ""
		}

		def, err := ix.ResolveType(ctx, pf, key)
		if err != nil || def == nil {
			return ""
		}

		switch def.Kind {
		case DefinitionEnum:
			return ""
		case DefinitionStruct, DefinitionUnion, DefinitionException:
			return kindLabel(def.Kind)
		case DefinitionTypedef:
			td, ok := def.Node.(*syntax.Typedef)
			if !ok {
				return ""
			}

			return s.mapKeyKind(ctx, view, ix, def.Parsed, td.Type, depth+1)
		}
	}

	return ""
}

// kindLabel is the message label of a definition kind.
func kindLabel(k DefinitionKind) string {
	switch k {
	case DefinitionStruct:
		return "struct"
	case DefinitionUnion:
		return "union"
	case DefinitionException:
		return "exception"
	}

	return "type"
}
