package sema

import (
	"context"
	"fmt"
	"path"
	"path/filepath"

	"github.com/karitham/thrift-ls/syntax"
)

// AddIncludeFixer offers the include that makes an undefined type resolve:
// the definition is searched across the workspace, so the fix works
// project-wide. It fixes only "undefined-type" diagnostics.
type AddIncludeFixer struct{}

func (f AddIncludeFixer) Fix(ctx context.Context, file File, d Diagnostic) []Fix {
	if d.Code != CodeUndefinedType {
		return nil
	}

	// A file with parse errors is not safely editable.
	if len(file.PF.Errors()) > 0 {
		return nil
	}

	name := undefinedTypeName(file.PF, d.Span)
	if name == "" {
		return nil
	}

	def, err := file.Index().FindInWorkspace(ctx, name)
	if err != nil || def == nil || def.File == file.URI {
		return nil
	}

	incPath, err := filepath.Rel(path.Dir(file.URI.Path()), def.File.Path())
	if err != nil {
		return nil
	}

	incPath = filepath.ToSlash(incPath)

	content, err := file.PF.Content()
	if err != nil {
		return nil
	}

	// Insert after the last include statement, or at the top of the file.
	insert := syntax.Position{Line: 1, Col: 1}
	if includes := file.PF.AST().Includes(); len(includes) > 0 {
		last := includes[len(includes)-1]
		_, end := file.PF.AST().Range(last)
		insert = lineSpan(content, end).End
	}

	return []Fix{{
		Title: fmt.Sprintf("Add include %q", incPath),
		Edits: []Edit{{
			Span:    Span{Start: insert, End: insert},
			NewText: fmt.Sprintf("include %q\n", incPath),
		}},
	}}
}

// undefinedTypeName returns the type name the "undefined-type" diagnostic
// at sp points at: the identifier whose reference failed to resolve. The
// diagnostic's span covers that identifier exactly.
func undefinedTypeName(pf interface {
	AST() *syntax.Document
}, sp Span) string {
	path := pf.AST().SearchNodePathByPosition(sp.Start)
	if len(path) < 2 {
		return ""
	}

	id, ok := path[len(path)-1].(*syntax.Identifier)
	if !ok {
		return ""
	}

	if ft, ok := path[len(path)-2].(*syntax.FieldType); ok && ft.Ident == id {
		return TypeReferenceName(ft)
	}

	return ""
}
