package sema

import (
	"context"
	"fmt"
	"strconv"

	"github.com/karitham/thrift-ls/store"
	"github.com/karitham/thrift-ls/syntax"
)

// DuplicateCheck reports the duplicates the compiler rejects: a name
// defined twice in one scope — structs, unions, exceptions, enums, members,
// fields, function arguments, service functions, typedefs, constants — enum
// members whose resolved value collides, and map keys or set values that
// repeat in a constant. Duplicates are ambiguous on the wire and in
// generated code, so they are errors.
type DuplicateCheck struct{}

func (c *DuplicateCheck) Name() string {
	return "DuplicateCheck"
}

func (c *DuplicateCheck) AnalyzeFile(ctx context.Context, f File) ([]Diagnostic, error) {
	pf := f.PF

	var ret []Diagnostic

	// Top-level definitions share one scope.
	var defs []named

	for _, st := range pf.AST().Structs() {
		defs = append(defs, named{"struct", st.Name})
	}

	for _, u := range pf.AST().Unions() {
		defs = append(defs, named{"union", u.Name})
	}

	for _, ex := range pf.AST().Exceptions() {
		defs = append(defs, named{"exception", ex.Name})
	}

	for _, en := range pf.AST().Enums() {
		defs = append(defs, named{"enum", en.Name})
	}

	for _, td := range pf.AST().Typedefs() {
		defs = append(defs, named{"typedef", td.Name})
	}

	for _, cs := range pf.AST().Consts() {
		defs = append(defs, named{"const", cs.Name})
	}

	for _, sv := range pf.AST().Services() {
		defs = append(defs, named{"service", sv.Name})
	}

	ret = append(ret, checkNames(pf, defs)...)

	for _, enum := range pf.AST().Enums() {
		var members []named

		for _, v := range enum.Values {
			members = append(members, named{"member", v.Name})
		}

		ret = append(ret, checkNames(pf, members)...)
		ret = append(ret, checkEnumValues(pf, enum)...)
	}

	structLikes := [][]*syntax.Field{}
	for _, st := range pf.AST().Structs() {
		structLikes = append(structLikes, st.Fields)
	}

	for _, u := range pf.AST().Unions() {
		structLikes = append(structLikes, u.Fields)
	}

	for _, ex := range pf.AST().Exceptions() {
		structLikes = append(structLikes, ex.Fields)
	}

	for _, fields := range structLikes {
		ret = append(ret, checkNames(pf, fieldNames(fields, "field"))...)
	}

	for _, sv := range pf.AST().Services() {
		var fns []named
		for _, fn := range sv.Functions {
			fns = append(fns, named{"function", fn.Name})
		}

		ret = append(ret, checkNames(pf, fns)...)

		for _, fn := range sv.Functions {
			ret = append(ret, checkNames(pf, fieldNames(fn.Args, "argument"))...)

			if fn.Throws != nil {
				ret = append(ret, checkNames(pf, fieldNames(fn.Throws.Fields, "field"))...)
			}
		}
	}

	for _, cs := range pf.AST().Consts() {
		ret = append(ret, checkValueDuplicates(pf, cs.Value, cs.Type)...)
	}

	pf.AST().WalkFieldLists(func(fields []*syntax.Field, _ syntax.FieldListKind) {
		for _, field := range fields {
			if field.Value != nil {
				ret = append(ret, checkValueDuplicates(pf, field.Value, field.Type)...)
			}
		}
	})

	return ret, nil
}

// named is a definition with the label its messages use.
type named struct {
	kind string
	id   *syntax.Identifier
}

// fieldNames labels every field of a scope with the same kind.
func fieldNames(fields []*syntax.Field, kind string) []named {
	out := make([]named, 0, len(fields))

	for _, f := range fields {
		out = append(out, named{kind, f.Name})
	}

	return out
}

// checkNames reports a diagnostic on every name in a scope that repeats an
// earlier one.
func checkNames(pf *store.ParsedFile, defs []named) []Diagnostic {
	seen := make(map[string]bool)

	var ret []Diagnostic

	for _, d := range defs {
		if d.id == nil || d.id.Text == "" {
			continue
		}

		if seen[d.id.Text] {
			ret = append(ret, Diagnostic{
				Span:     spanOfToken(nameToken(pf, d.id)),
				Severity: SeverityError,
				Code:     CodeDuplicateDef,
				Message:  fmt.Sprintf("duplicate %s %s", d.kind, d.id.Text),
			})
			continue
		}

		seen[d.id.Text] = true
	}

	return ret
}

// nameToken returns the token of an identifier.
func nameToken(pf *store.ParsedFile, id *syntax.Identifier) *syntax.Token {
	return &pf.AST().Tokens[id.TokStart()]
}

// checkEnumValues reports enum members whose resolved value — an explicit
// constant or the compiler's auto-increment — collides with an earlier
// member's.
func checkEnumValues(pf *store.ParsedFile, enum *syntax.Enum) []Diagnostic {
	seen := make(map[int64]string) // value -> first member with it

	var ret []Diagnostic

	for _, mv := range enumValues(enum) {
		if !mv.known {
			continue
		}

		if first, ok := seen[mv.value]; ok {
			ret = append(ret, Diagnostic{
				Span:     spanOfToken(enumValueNameToken(pf, mv.member)),
				Severity: SeverityError,
				Code:     CodeDuplicateEnumVal,
				Message:  fmt.Sprintf("enum value %d duplicates %s", mv.value, first),
			})
			continue
		}

		seen[mv.value] = mv.member.Name.Text
	}

	return ret
}

// checkValueDuplicates reports repeated keys in map literals and repeated
// values in set literals, recursing into nested containers. The declared
// type decides whether a list literal is a set: lists keep duplicates,
// sets reject them.
func checkValueDuplicates(pf *store.ParsedFile, value *syntax.ConstValue, typ *syntax.FieldType) []Diagnostic {
	var ret []Diagnostic

	switch value.Kind {
	case syntax.ValueMap:
		seen := make(map[string]bool)

		for _, entry := range value.Map {
			if entry.Key != nil {
				if seen[valueKey(entry.Key)] {
					ret = append(ret, duplicateValueDiagnostic(pf, entry.Key, "map key"))
				}
				seen[valueKey(entry.Key)] = true
			}

			if entry.Value != nil {
				ret = append(ret, checkValueDuplicates(pf, entry.Value, containerValueType(typ))...)
			}
		}
	case syntax.ValueList:
		if typ != nil && typ.Kind == syntax.TypeSet {
			seen := make(map[string]bool)

			for _, item := range value.List {
				if seen[valueKey(item)] {
					ret = append(ret, duplicateValueDiagnostic(pf, item, "set value"))
				}
				seen[valueKey(item)] = true
			}
		}

		for _, item := range value.List {
			ret = append(ret, checkValueDuplicates(pf, item, containerValueType(typ))...)
		}
	}

	return ret
}

// containerValueType returns the element type of a container type — the
// value type of maps, the value type of lists and sets — or nil when typ
// is not a container.
func containerValueType(typ *syntax.FieldType) *syntax.FieldType {
	if typ == nil {
		return nil
	}

	switch typ.Kind {
	case syntax.TypeMap, syntax.TypeList, syntax.TypeSet:
		return typ.ValueType
	}

	return nil
}

// valueKey is the canonical identity of a constant value for duplicate
// detection: strings by their content, integers by their numeric value,
// everything else by source text.
func valueKey(v *syntax.ConstValue) string {
	switch v.Kind {
	case syntax.ValueString:
		if len(v.Text) >= 2 && v.Text[0] == v.Text[len(v.Text)-1] &&
			(v.Text[0] == '"' || v.Text[0] == '\'') {
			return v.Text[1 : len(v.Text)-1]
		}

		return v.Text
	case syntax.ValueInt:
		if n, err := strconv.ParseInt(v.Text, 0, 64); err == nil {
			return "int:" + strconv.FormatInt(n, 10)
		}
	}

	return v.Text
}

// duplicateValueDiagnostic is the diagnostic for a repeated map key or set
// value.
func duplicateValueDiagnostic(pf *store.ParsedFile, v *syntax.ConstValue, kind string) Diagnostic {
	return Diagnostic{
		Span:     spanOfToken(&pf.AST().Tokens[v.TokStart()]),
		Severity: SeverityError,
		Code:     CodeDuplicateValue,
		Message:  fmt.Sprintf("duplicate %s %s", kind, v.Text),
	}
}
