package completion

import (
	"context"
	"path/filepath"
	"sort"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

// Provider supplies completion candidates for one grammar slot. Prefix
// filtering, sorting, the edit range, and the item cap stay in the shared
// pipeline (TokenCompletion.Completion).
type Provider interface {
	Kind() ContextKind

	// Candidates returns unfiltered candidates for the slot. The current
	// context carries the prefix and the document.
	Candidates(ctx context.Context, ss *cache.Snapshot, file uri.URI, c Context) []Candidate
}

// providersFor returns the providers for a slot: the exact slot provider
// first, then supplementary providers (e.g. base type keywords on type
// positions). Unknown or intentionally empty slots (CtxNone, CtxFieldID,
// CtxDefinitionName, CtxFunctionName, CtxAnnotationValue) return nothing.
func providersFor(kind ContextKind) []Provider {
	switch kind {
	case CtxIncludePath:
		return []Provider{includeProvider{}}
	case CtxType:
		return []Provider{typeProvider{}, keywordProvider{}}
	case CtxFieldValue:
		return []Provider{valueProvider{}}
	case CtxFieldName:
		return []Provider{fieldNameProvider{}}
	case CtxEnumValueName:
		return []Provider{valueProvider{}}
	case CtxAnnotationKey:
		return []Provider{annotationKeyProvider{}}
	case CtxServiceExtends:
		return []Provider{serviceExtendsProvider{}}
	case CtxKeyword:
		return []Provider{keywordProvider{}}
	default:
		return nil
	}
}

type includeProvider struct{}

func (includeProvider) Kind() ContextKind { return CtxIncludePath }

func (includeProvider) Candidates(_ context.Context, ss *cache.Snapshot, file uri.URI, c Context) []Candidate {
	return ListDirAndFiles(filepath.Dir(file.Path()), ss.Resolver().IncludePaths(), c.Prefix)
}

type typeProvider struct{}

func (typeProvider) Kind() ContextKind { return CtxType }

func (typeProvider) Candidates(ctx context.Context, ss *cache.Snapshot, file uri.URI, c Context) []Candidate {
	return typeCandidates(ctx, ss, file, c.Doc)
}

type valueProvider struct{}

func (valueProvider) Kind() ContextKind { return CtxFieldValue }

func (valueProvider) Candidates(ctx context.Context, ss *cache.Snapshot, file uri.URI, c Context) []Candidate {
	return valueCandidates(ctx, ss, file, c.Doc)
}

type keywordProvider struct{}

func (keywordProvider) Kind() ContextKind { return CtxKeyword }

// Candidates returns the keyword snippets and every identifier token known
// to the file (and its includes).
func (keywordProvider) Candidates(_ context.Context, ss *cache.Snapshot, file uri.URI, _ Context) []Candidate {
	res := make([]Candidate, 0, len(keywords)+16)

	for text, format := range keywords {
		res = append(res, Candidate{showText: text, insertText: text, format: format})
	}

	for text := range ss.TokensForFile(file) {
		res = append(res, Candidate{showText: text, insertText: text, format: protocol.InsertTextFormatPlainText})
	}

	return res
}

type fieldNameProvider struct{}

func (fieldNameProvider) Kind() ContextKind { return CtxFieldName }

// Candidates returns the field modifiers and every identifier token, so a
// field name position suggests required/optional and names from the
// codebase — never value candidates.
func (fieldNameProvider) Candidates(_ context.Context, ss *cache.Snapshot, file uri.URI, _ Context) []Candidate {
	res := []Candidate{
		{showText: "required", insertText: "required", format: protocol.InsertTextFormatPlainText},
		{showText: "optional", insertText: "optional", format: protocol.InsertTextFormatPlainText},
	}

	for text := range ss.TokensForFile(file) {
		res = append(res, Candidate{showText: text, insertText: text, format: protocol.InsertTextFormatPlainText})
	}

	return res
}

type annotationKeyProvider struct{}

func (annotationKeyProvider) Kind() ContextKind { return CtxAnnotationKey }

// Candidates collects the annotation names used in the file and its
// transitively included files.
func (annotationKeyProvider) Candidates(ctx context.Context, ss *cache.Snapshot, file uri.URI, c Context) []Candidate {
	keys := make(map[string]struct{})

	collect := func(doc *syntax.Document) {
		for key := range annotationKeys(doc) {
			keys[key] = struct{}{}
		}
	}

	collect(c.Doc)

	for _, inc := range includedFiles(ss, file) {
		if pf, err := ss.Parse(ctx, inc); err == nil && pf.AST() != nil {
			collect(pf.AST())
		}
	}

	var res []Candidate
	for key := range keys {
		res = append(res, Candidate{showText: key, insertText: key, format: protocol.InsertTextFormatPlainText})
	}

	sortCandidates(res)

	return res
}

// annotationKeys collects the names of every annotation in the document:
// on definitions, fields, enum values, functions, namespaces, and typedefs.
func annotationKeys(doc *syntax.Document) map[string]struct{} {
	keys := make(map[string]struct{})

	add := func(annotations *syntax.Annotations) {
		if annotations == nil {
			return
		}

		for _, a := range annotations.Items {
			keys[a.Name.Text] = struct{}{}
		}
	}

	for _, ns := range doc.Namespaces() {
		add(ns.Annotations)
	}

	for _, td := range doc.Typedefs() {
		add(td.Annotations)
	}

	for _, cst := range doc.Consts() {
		_ = cst // consts carry no annotations
	}

	for _, st := range doc.Structs() {
		add(st.Annotations)

		for _, f := range st.Fields {
			add(f.Annotations)
		}
	}

	for _, enum := range doc.Enums() {
		add(enum.Annotations)

		for _, v := range enum.Values {
			add(v.Annotations)
		}
	}

	for _, svc := range doc.Services() {
		add(svc.Annotations)

		for _, fn := range svc.Functions {
			add(fn.Annotations)
		}
	}

	return keys
}

type serviceExtendsProvider struct{}

func (serviceExtendsProvider) Kind() ContextKind { return CtxServiceExtends }

// Candidates returns the service names from the file and its includes.
func (serviceExtendsProvider) Candidates(ctx context.Context, ss *cache.Snapshot, file uri.URI, c Context) []Candidate {
	names := make(map[string]struct{})

	collect := func(doc *syntax.Document) {
		for _, svc := range doc.Services() {
			names[svc.Name.Text] = struct{}{}
		}
	}

	collect(c.Doc)

	for _, inc := range includedFiles(ss, file) {
		if pf, err := ss.Parse(ctx, inc); err == nil && pf.AST() != nil {
			collect(pf.AST())
		}
	}

	var res []Candidate
	for name := range names {
		res = append(res, Candidate{showText: name, insertText: name, format: protocol.InsertTextFormatPlainText})
	}

	sortCandidates(res)

	return res
}

// sortCandidates sorts candidates alphabetically by show text.
func sortCandidates(res []Candidate) {
	sort.Slice(res, func(i, j int) bool { return res[i].showText < res[j].showText })
}
