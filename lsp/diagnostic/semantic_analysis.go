package diagnostic

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/codejump"
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

	res := s.checkDefineConflict(ctx, pf)
	items := s.checkDefinitionExist(ctx, ss, changeFile, pf)
	res = append(res, items...)

	return res, nil
}

// checkDefineConflict reports duplicate field names, function names, and
// top-level definition names.
func (s *SemanticAnalysis) checkDefineConflict(ctx context.Context, pf *cache.ParsedFile) []protocol.Diagnostic {
	var ret []protocol.Diagnostic

	processStructLike := func(fields []*syntax.Field) {
		fieldMap := make(map[string]struct{})
		for i := range fields {
			field := fields[i]
			if _, exist := fieldMap[field.Name.Text]; exist {
				ret = append(ret, protocol.Diagnostic{
					Range:    nodeRange(pf.AST(), field.Name),
					Severity: protocol.DiagnosticSeverityError,
					Source:   protocol.NewOptional("thrift-ls"),
					Message:  protocol.String("field name conflict with other field"),
				})
			}
			fieldMap[field.Name.Text] = struct{}{}
		}
	}

	definitionNameMap := make(map[string]string)

	processDefinition := func(name string, node syntax.Node, kind string) {
		if previous, exist := definitionNameMap[name]; exist {
			ret = append(ret, protocol.Diagnostic{
				Range:    nodeRange(pf.AST(), node),
				Severity: protocol.DiagnosticSeverityError,
				Source:   protocol.NewOptional("thrift-ls"),
				Message:  protocol.String(fmt.Sprintf("%s name conflict with other %s", kind, previous)),
			})
		}
		definitionNameMap[name] = kind
	}

	for _, st := range pf.AST().Structs() {
		processDefinition(st.Name.Text, st.Name, "struct")
		processStructLike(st.Fields)
	}
	for _, union := range pf.AST().Unions() {
		processDefinition(union.Name.Text, union.Name, "union")
		processStructLike(union.Fields)
	}
	for _, excep := range pf.AST().Exceptions() {
		processDefinition(excep.Name.Text, excep.Name, "exception")
		processStructLike(excep.Fields)
	}
	for _, enum := range pf.AST().Enums() {
		processDefinition(enum.Name.Text, enum.Name, "enum")
	}
	for _, cst := range pf.AST().Consts() {
		processDefinition(cst.Name.Text, cst.Name, "const")
	}
	for _, td := range pf.AST().Typedefs() {
		processDefinition(td.Name.Text, td.Name, "typedef")
	}

	for _, svc := range pf.AST().Services() {
		processDefinition(svc.Name.Text, svc.Name, "service")

		fnMap := make(map[string]struct{})
		for _, fn := range svc.Functions {
			if _, exist := fnMap[fn.Name.Text]; exist {
				ret = append(ret, protocol.Diagnostic{
					Range:    nodeRange(pf.AST(), fn.Name),
					Severity: protocol.DiagnosticSeverityWarning,
					Source:   protocol.NewOptional("thrift-ls"),
					Message:  protocol.String("function name conflict with other function"),
				})
			}
			fnMap[fn.Name.Text] = struct{}{}
			processStructLike(fn.Args)
			if fn.Throws != nil {
				processStructLike(fn.Throws.Fields)
			}
		}
	}

	return ret
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

	for _, st := range pf.AST().Structs() {
		processStructLike(st.Fields)
	}
	for _, union := range pf.AST().Unions() {
		processStructLike(union.Fields)
	}
	for _, excep := range pf.AST().Exceptions() {
		processStructLike(excep.Fields)
	}
	for _, cst := range pf.AST().Consts() {
		items := s.checkConstValueExist(ctx, ss, file, pf, cst.Value)
		ret = append(ret, items...)
	}
	for _, svc := range pf.AST().Services() {
		for _, fn := range svc.Functions {
			items := s.checkTypeExist(ctx, ss, file, pf, fn.Type)
			ret = append(ret, items...)

			processStructLike(fn.Args)
			if fn.Throws != nil {
				processStructLike(fn.Throws.Fields)
			}
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

	_, id, err := codejump.FindConstValueDefinition(ctx, ss, file, pf.AST(), cst)
	if err != nil || id == nil {
		res = append(res, protocol.Diagnostic{
			Range:    nodeRange(pf.AST(), cst),
			Severity: protocol.DiagnosticSeverityError,
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
			return mismatchDiagnostic(pf.AST(), field, expect, kindName(valueKind))
		}
	case syntax.ValueInt:
		// true/false lex as int constants but are bools.
		if value.Text == "true" || value.Text == "false" {
			if expect != "bool" {
				return mismatchDiagnostic(pf.AST(), field, expect, "bool")
			}
			return nil
		}
		switch expect {
		case "i8", "i16", "i32", "i64":
		default:
			return mismatchDiagnostic(pf.AST(), field, expect, "i64")
		}
	case syntax.ValueIdent:
		if expect == "bool" {
			return mismatchDiagnostic(pf.AST(), field, expect, "identifier")
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

func mismatchDiagnostic(doc *syntax.Document, field *syntax.Field, expect, got string) *protocol.Diagnostic {
	return &protocol.Diagnostic{
		Range:    nodeRange(doc, field.Value),
		Severity: protocol.DiagnosticSeverityError,
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
		_, id, _, err := codejump.FindTypeDefinition(ctx, ss, file, pf.AST(), ft)
		if err != nil || id == nil {
			res = append(res, protocol.Diagnostic{
				Range:    nodeRange(pf.AST(), ft.Ident),
				Severity: protocol.DiagnosticSeverityError,
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
	}
	if ft.ValueType != nil {
		res = append(res, s.checkTypeExist(ctx, ss, file, pf, ft.ValueType)...)
	}
	return res
}
