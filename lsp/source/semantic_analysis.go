package source

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

type SemanticAnalysis struct{}

func (s *SemanticAnalysis) Diagnostic(ctx context.Context, ss *cache.Snapshot, changeFiles []uri.URI) (DiagnosticResult, error) {
	res := make(DiagnosticResult)

	for _, file := range changeFiles {
		items, err := s.diagnostic(ctx, ss, file)
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

func (s *SemanticAnalysis) diagnostic(ctx context.Context, ss *cache.Snapshot, changeFile uri.URI) ([]protocol.Diagnostic, error) {
	pf, err := ss.Parse(ctx, changeFile)
	if err != nil {
		return nil, err
	}

	if pf.AST() == nil {
		return nil, errors.New("parse ast failed")
	}

	for _, err := range pf.Errors() {
		slog.Debug("parse failed", "err", err)
	}

	res := s.checkDefinitionExist(ctx, ss, changeFile, pf)

	return res, nil
}

// checkDefinitionExist reports field types, const values, and return types
// that reference undefined definitions.
func (s *SemanticAnalysis) checkDefinitionExist(ctx context.Context, ss *cache.Snapshot, file uri.URI, pf *cache.ParsedFile) []protocol.Diagnostic {
	ret := make([]protocol.Diagnostic, 0)

	processStructLike := func(fields []*syntax.Field) {
		for i := range fields {
			field := fields[i]
			items := s.checkTypeExist(ctx, ss, file, pf, field.Type)
			ret = append(ret, items...)

			if field.Value != nil {
				items := s.checkConstValueExist(ctx, ss, file, pf, field.Value)
				ret = append(ret, items...)

				dig := s.checkConstValueMatchType(pf, field)
				if dig != nil {
					ret = append(ret, *dig)
				}
			}
		}
	}

	pf.AST().WalkFieldLists(func(fields []*syntax.Field, _ syntax.FieldListKind) {
		processStructLike(fields)
	})

	for _, cst := range pf.AST().Consts() {
		items := s.checkConstValueExist(ctx, ss, file, pf, cst.Value)
		ret = append(ret, items...)
	}

	for _, svc := range pf.AST().Services() {
		for _, fn := range svc.Functions {
			items := s.checkTypeExist(ctx, ss, file, pf, fn.Type)
			ret = append(ret, items...)
		}
	}

	return ret
}

func (s *SemanticAnalysis) checkConstValueExist(ctx context.Context, ss *cache.Snapshot,
	file uri.URI, pf *cache.ParsedFile, cst *syntax.ConstValue,
) (res []protocol.Diagnostic) {
	if cst == nil || cst.Kind != syntax.ValueIdent {
		return res
	}

	if cst.Text == "true" || cst.Text == "false" {
		return res
	}

	_, id, err := FindConstValueDefinition(ctx, ss, file, pf.AST(), cst)
	if err != nil || id == nil {
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

func (s *SemanticAnalysis) checkTypeExist(ctx context.Context, ss *cache.Snapshot,
	file uri.URI, pf *cache.ParsedFile, ft *syntax.FieldType,
) (res []protocol.Diagnostic) {
	if ft == nil {
		return res
	}

	switch ft.Kind {
	case syntax.TypeMap, syntax.TypeList, syntax.TypeSet:
		return s.checkContainerTypeExist(ctx, ss, file, pf, ft)
	case syntax.TypeBase:
		return nil
	case syntax.TypeIdent:
		_, id, _, err := FindTypeDefinition(ctx, ss, file, pf.AST(), ft)
		if err != nil || id == nil {
			res = append(res, protocol.Diagnostic{
				Range:    nodeRange(pf, ft.Ident),
				Severity: protocol.DiagnosticSeverityError,
				Code:     protocol.String(CodeUndefinedType),
				Source:   protocol.NewOptional("thrift-ls"),
				Message:  protocol.String("field type doesn't exist"),
			})
		}
	}

	return res
}

func (s *SemanticAnalysis) checkContainerTypeExist(ctx context.Context,
	ss *cache.Snapshot, file uri.URI, pf *cache.ParsedFile, ft *syntax.FieldType,
) (res []protocol.Diagnostic) {
	if ft.KeyType != nil {
		res = append(res, s.checkTypeExist(ctx, ss, file, pf, ft.KeyType)...)

		if ft.Kind == syntax.TypeMap {
			if dig := s.checkMapKeyScalar(ctx, ss, file, pf, ft.KeyType); dig != nil {
				res = append(res, *dig)
			}
		}
	}

	if ft.ValueType != nil {
		res = append(res, s.checkTypeExist(ctx, ss, file, pf, ft.ValueType)...)
	}

	return res
}

// checkMapKeyScalar returns an error when the map key type is not scalar:
// thrift requires map keys to be a base type or an enum. Structs, unions,
// exceptions, and containers cannot be keys; typedefs are followed.
func (s *SemanticAnalysis) checkMapKeyScalar(ctx context.Context, ss *cache.Snapshot, file uri.URI, pf *cache.ParsedFile, key *syntax.FieldType) *protocol.Diagnostic {
	kind := s.mapKeyKind(ctx, ss, file, pf.AST(), key, 0)
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
func (s *SemanticAnalysis) mapKeyKind(ctx context.Context, ss *cache.Snapshot, file uri.URI, ast *syntax.Document, key *syntax.FieldType, depth int) string {
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

		dstFile, id, kind, err := FindTypeDefinition(ctx, ss, file, ast, key)
		if err != nil || id == nil {
			return ""
		}

		switch kind {
		case DefinitionEnum:
			return ""
		case DefinitionStruct, DefinitionUnion, DefinitionException:
			return kindLabel(kind)
		case DefinitionTypedef:
			dstPf, err := parseDefinitionFile(ctx, ss, dstFile)
			if err != nil {
				return ""
			}

			td, ok := dstPf.Definitions()[id.Text].(*syntax.Typedef)
			if !ok {
				return ""
			}

			return s.mapKeyKind(ctx, ss, dstFile, dstPf.AST(), td.Type, depth+1)
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
