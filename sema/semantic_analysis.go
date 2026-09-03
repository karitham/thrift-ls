package sema

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/karitham/thrift-ls/store"
	"github.com/karitham/thrift-ls/syntax"
)

type SemanticAnalysis struct{}

func (s *SemanticAnalysis) Name() string {
	return "SemanticAnalysis"
}

func (s *SemanticAnalysis) AnalyzeFile(ctx context.Context, f File) ([]Diagnostic, error) {
	// The run's shared index: resolutions are memoized per (file, name),
	// across every analyzer in the run.
	return s.checkDefinitionExist(ctx, f.View(), f.Index(), f.PF), nil
}

// checkDefinitionExist reports undefined references in one document walk:
// type references that resolve to no definition (fields, signatures,
// consts, typedefs, nested container elements), constant-value identifiers
// resolving to no enum value or const at any nesting depth, and field defaults
// or const values whose kind — at any literal depth — mismatches the declared
// type's underlying kind.
func (s *SemanticAnalysis) checkDefinitionExist(ctx context.Context, view Graph, ix *Index, pf *store.ParsedFile) []Diagnostic {
	res := make([]Diagnostic, 0)

	syntax.Walk(pf.AST(), func(n syntax.Node) bool {
		switch v := n.(type) {
		case *syntax.FieldType:
			res = append(res, s.checkTypeExist(ctx, view, ix, pf, v)...)
		case *syntax.Field:
			if v.Value != nil {
				res = append(res, s.checkValueType(ctx, ix, pf, pf, v.Type, v.Value)...)
			}
		case *syntax.Const:
			res = append(res, s.checkValueType(ctx, ix, pf, pf, v.Type, v.Value)...)
		case *syntax.ConstValue:
			res = append(res, s.checkConstValueExist(ctx, view, ix, pf, v)...)
		case *syntax.StructuredAnnotation:
			res = append(res, s.checkAnnotationTypeExist(ctx, view, ix, pf, v)...)
		}

		return true
	})

	return res
}

// checkAnnotationTypeExist reports a structured annotation whose name
// resolves to no definition. Matching the upfluence compiler, the name of
// a structured annotation must be a declared type: the compiler errors at
// parse time ("Type %s does not exist"); here the semantic pass owns it.
func (s *SemanticAnalysis) checkAnnotationTypeExist(ctx context.Context, view Graph, ix *Index,
	pf *store.ParsedFile, sa *syntax.StructuredAnnotation,
) (res []Diagnostic) {
	if sa == nil || sa.Name == nil {
		return res
	}

	ft := &syntax.FieldType{Kind: syntax.TypeIdent, Ident: sa.Name}

	def, err := ix.ResolveType(ctx, pf, ft)
	if err == nil && def != nil {
		return res
	}

	return append(res, Diagnostic{
		Span:     SpanOf(pf, sa.Name),
		Severity: SeverityError,
		Code:     CodeUnknownAnnotation,
		Message:  "annotation type doesn't exist",
	})
}

func (s *SemanticAnalysis) checkConstValueExist(ctx context.Context, view Graph, ix *Index,
	pf *store.ParsedFile, cst *syntax.ConstValue,
) (res []Diagnostic) {
	if cst == nil || cst.Kind != syntax.ValueIdent {
		return res
	}

	if cst.Text == "true" || cst.Text == "false" {
		return res
	}

	def, err := ix.ResolveValue(ctx, pf, cst)
	if err != nil || def == nil {
		res = append(res, Diagnostic{
			Span:     SpanOf(pf, cst),
			Severity: SeverityError,
			Code:     CodeUndefinedValue,
			Message:  "default value doesn't exist",
		})
	}

	return res
}

// checkValueType reports a field default or const value whose literal
// contradicts the declared type's underlying kind: typedef chains are
// followed (across includes) before comparing, so "typedef map<string,
// string> M" accepts map literals, and the comparison recurses — container
// entries and struct field values are each classified against their own
// resolved type. reportPf backs the literal's ranges; scopePf is the file
// whose scope expect resolves in — the two part ways once the type walks
// into an include. Existence of identifier values is checkConstValueExist's
// job; this only classifies kinds.
func (s *SemanticAnalysis) checkValueType(ctx context.Context, ix *Index, reportPf, scopePf *store.ParsedFile, expect *syntax.FieldType, value *syntax.ConstValue) []Diagnostic {
	if value == nil {
		return nil
	}

	ut, typePf := ix.UnderlyingType(ctx, scopePf, expect)
	if ut == nil {
		// The declared type doesn't resolve; checkTypeExist already
		// reports it, and no kind comparison is meaningful.
		return nil
	}

	switch value.Kind {
	case syntax.ValueMap:
		if ut.Kind == syntax.TypeMap {
			return s.checkMapEntries(ctx, ix, reportPf, typePf, ut, value)
		}

		if fields, structPf := structFields(ctx, ix, ut, typePf); fields != nil {
			return s.checkStructEntries(ctx, ix, reportPf, underlyingName(ut), fields, structPf, value)
		}

	case syntax.ValueList:
		if isKind(ut, syntax.TypeList, syntax.TypeSet) {
			return s.checkListEntries(ctx, ix, reportPf, typePf, ut, value)
		}

	case syntax.ValueString:
		if isBase(ut, syntax.TokenString) {
			return nil
		}

	case syntax.ValueDouble:
		if isBase(ut, syntax.TokenDouble) {
			return nil
		}

	case syntax.ValueInt:
		if intValueMatches(ctx, ix, ut, typePf, value) {
			return nil
		}

	case syntax.ValueIdent:
		if identValueMatches(ctx, ix, reportPf, ut, value) {
			return nil
		}
	}

	return kindMismatch(reportPf, value, ut)
}

// checkMapEntries validates each entry of a map literal against the map
// type's resolved key and value types.
func (s *SemanticAnalysis) checkMapEntries(ctx context.Context, ix *Index, reportPf, scopePf *store.ParsedFile, ut *syntax.FieldType, value *syntax.ConstValue) []Diagnostic {
	var res []Diagnostic

	for _, entry := range value.Map {
		res = append(res, s.checkValueType(ctx, ix, reportPf, scopePf, ut.KeyType, entry.Key)...)
		res = append(res, s.checkValueType(ctx, ix, reportPf, scopePf, ut.ValueType, entry.Value)...)
	}

	return res
}

// checkListEntries validates each element of a list literal against the
// container's resolved element type.
func (s *SemanticAnalysis) checkListEntries(ctx context.Context, ix *Index, reportPf, scopePf *store.ParsedFile, ut *syntax.FieldType, value *syntax.ConstValue) []Diagnostic {
	var res []Diagnostic

	for _, elem := range value.List {
		res = append(res, s.checkValueType(ctx, ix, reportPf, scopePf, ut.ValueType, elem)...)
	}

	return res
}

// checkStructEntries validates a struct-valued map literal: keys must name
// fields of the resolved definition, and each value is classified against
// the field's own type.
func (s *SemanticAnalysis) checkStructEntries(ctx context.Context, ix *Index, reportPf *store.ParsedFile, typeName string, fields []*syntax.Field, structPf *store.ParsedFile, value *syntax.ConstValue) []Diagnostic {
	byName := make(map[string]*syntax.Field, len(fields))
	for _, f := range fields {
		if f.Name != nil {
			byName[f.Name.Text] = f
		}
	}

	var res []Diagnostic

	for _, entry := range value.Map {
		if entry.Key.Kind != syntax.ValueString {
			res = append(res, *mismatchDiagnostic(reportPf, entry.Key, "field name", kindName(entry.Key.Kind)))
			continue
		}

		name := strings.Trim(entry.Key.Text, "'\"")

		f, ok := byName[name]
		if !ok {
			res = append(res, unknownFieldDiagnostic(reportPf, entry.Key, typeName, name))
			continue
		}

		res = append(res, s.checkValueType(ctx, ix, reportPf, structPf, f.Type, entry.Value)...)
	}

	return res
}

// kindMismatch reports a literal whose kind contradicts the target type.
func kindMismatch(pf *store.ParsedFile, value *syntax.ConstValue, ut *syntax.FieldType) []Diagnostic {
	return []Diagnostic{*mismatchDiagnostic(pf, value, underlyingName(ut), gotName(value))}
}

// gotName names a literal's kind for a mismatch message. true/false lex as
// int constants but are bools.
func gotName(value *syntax.ConstValue) string {
	if value.Kind == syntax.ValueInt && (value.Text == "true" || value.Text == "false") {
		return "bool"
	}

	return kindName(value.Kind)
}

// unknownFieldDiagnostic reports a struct-valued literal keyed by a name
// that is not a field of the definition. name is the key text with its
// quotes stripped.
func unknownFieldDiagnostic(pf *store.ParsedFile, key *syntax.ConstValue, typeName, name string) Diagnostic {
	return Diagnostic{
		Span:     SpanOf(pf, key),
		Severity: SeverityError,
		Code:     CodeValueTypeMismatch,
		Message:  fmt.Sprintf("no field named %q in %s", name, typeName),
	}
}

// isKind reports whether the underlying type is one of the container kinds.
func isKind(ft *syntax.FieldType, kinds ...syntax.FieldTypeKind) bool {
	if ft == nil {
		return false
	}

	return slices.Contains(kinds, ft.Kind)
}

// isBase reports whether the underlying type is the given base keyword.
func isBase(ft *syntax.FieldType, base syntax.TokenKind) bool {
	return ft != nil && ft.Kind == syntax.TypeBase && ft.Base == base
}

// structFields resolves the underlying type to a struct, union, or
// exception and returns its fields with the file whose scope the fields'
// types resolve in — the compiler accepts map literals, keyed by field
// name, for struct-valued constants and field defaults. nil fields when
// the type is not a struct-like definition.
func structFields(ctx context.Context, ix *Index, ut *syntax.FieldType, typePf *store.ParsedFile) ([]*syntax.Field, *store.ParsedFile) {
	if ut == nil || ut.Kind != syntax.TypeIdent {
		return nil, nil
	}

	def, err := ix.ResolveType(ctx, typePf, ut)
	if err != nil || def == nil || def.Parsed == nil {
		return nil, nil
	}

	s, ok := def.Node.(*syntax.Struct)
	if !ok {
		return nil, nil
	}

	return s.Fields, def.Parsed
}

// identValueMatches reports whether an identifier literal is acceptable in
// the target position. Any target takes an identifier: it may name a const
// or enum value of matching type, and existence is checkConstValueExist's
// job — an identifier resolving to nothing stays silent here. A bool
// target accepts only a reference to a const whose declared type is bool —
// the compiler substitutes bool-const identifiers before validating — so
// the reference must resolve, in the referencing file's scope, to a const
// of bool type.
func identValueMatches(ctx context.Context, ix *Index, pf *store.ParsedFile, ut *syntax.FieldType, value *syntax.ConstValue) bool {
	if !isBase(ut, syntax.TokenBool) {
		return true
	}

	def, err := ix.ResolveValue(ctx, pf, value)
	if err != nil || def == nil {
		return true
	}

	c, ok := def.Node.(*syntax.Const)
	if !ok {
		// An enum value reference is not a bool.
		return false
	}

	bt, _ := ix.UnderlyingType(ctx, def.Parsed, c.Type)

	return isBase(bt, syntax.TokenBool)
}

// intValueMatches reports whether an integer literal fits the underlying
// type: an integer base, or an enum — the compiler accepts int constants
// in enum positions.
func intValueMatches(ctx context.Context, ix *Index, ut *syntax.FieldType, typePf *store.ParsedFile, value *syntax.ConstValue) bool {
	if value.Text == "true" || value.Text == "false" {
		return isBase(ut, syntax.TokenBool)
	}

	if ut == nil {
		return false
	}

	switch ut.Kind {
	case syntax.TypeBase:
		switch ut.Base {
		case syntax.TokenByte, syntax.TokenI8, syntax.TokenI16, syntax.TokenI32, syntax.TokenI64:
			return true
		}
	case syntax.TypeIdent:
		def, err := ix.ResolveType(ctx, typePf, ut)
		return err == nil && def != nil && def.Kind == DefinitionEnum
	}

	return false
}

// underlyingName renders the underlying type for a mismatch message: the
// resolved container or base kind, or the unresolved identifier as written.
func underlyingName(ft *syntax.FieldType) string {
	if ft == nil {
		return "unknown"
	}

	switch ft.Kind {
	case syntax.TypeIdent:
		if ft.Ident != nil {
			return ft.Ident.Text
		}
	case syntax.TypeMap:
		return "map"
	case syntax.TypeList:
		return "list"
	case syntax.TypeSet:
		return "set"
	case syntax.TypeBase:
		return ft.Base.String()
	}

	return "unknown"
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

func mismatchDiagnostic(pf *store.ParsedFile, value *syntax.ConstValue, expect, got string) *Diagnostic {
	return &Diagnostic{
		Span:     SpanOf(pf, value),
		Severity: SeverityError,
		Code:     CodeValueTypeMismatch,
		Message:  fmt.Sprintf("expect %s but got %s", expect, got),
	}
}

// checkTypeExist reports a single type reference that resolves to no
// definition. The walk visits every FieldType individually — including
// container element types — so this needs no recursion.
func (s *SemanticAnalysis) checkTypeExist(ctx context.Context, view Graph, ix *Index,
	pf *store.ParsedFile, ft *syntax.FieldType,
) (res []Diagnostic) {
	if ft == nil || ft.Kind != syntax.TypeIdent {
		return res
	}

	def, err := ix.ResolveType(ctx, pf, ft)
	if err == nil && def != nil {
		return res
	}

	return append(res, Diagnostic{
		Span:     SpanOf(pf, ft.Ident),
		Severity: SeverityError,
		Code:     CodeUndefinedType,
		Message:  "field type doesn't exist",
	})
}
