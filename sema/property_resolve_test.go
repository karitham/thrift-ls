package sema_test

import (
	"context"
	"fmt"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/internal/proptest"
	"github.com/karitham/thrift-ls/sema"
	"github.com/karitham/thrift-ls/store"
	"github.com/karitham/thrift-ls/syntax"
)

// mdlNames is the definition name pool. Three names across up to five files
// guarantees same-named definitions in different files.
var mdlNames = []string{"T", "U", "N"}

// mdlEnumValues is the enum member name pool.
var mdlEnumValues = []string{"RED", "BLUE"}

// mdlKind is a definition kind in the model. It maps onto
// sema.DefinitionKind only at comparison time.
type mdlKind uint8

const (
	mdlStruct mdlKind = iota + 1
	mdlUnion
	mdlException
	mdlEnum
	mdlTypedef
	mdlConst
	mdlEnumValue
	mdlService
)

func (k mdlKind) semaKind() sema.DefinitionKind {
	switch k {
	case mdlStruct:
		return sema.DefinitionStruct
	case mdlUnion:
		return sema.DefinitionUnion
	case mdlException:
		return sema.DefinitionException
	case mdlEnum:
		return sema.DefinitionEnum
	case mdlTypedef:
		return sema.DefinitionTypedef
	case mdlConst:
		return sema.DefinitionConst
	case mdlEnumValue:
		return sema.DefinitionEnumValue
	case mdlService:
		return sema.DefinitionService
	}

	return sema.DefinitionNone
}

func (k mdlKind) String() string {
	switch k {
	case mdlStruct:
		return "struct"
	case mdlUnion:
		return "union"
	case mdlException:
		return "exception"
	case mdlEnum:
		return "enum"
	case mdlTypedef:
		return "typedef"
	case mdlConst:
		return "const"
	case mdlEnumValue:
		return "enum value"
	case mdlService:
		return "service"
	}

	return "none"
}

// mdlType is a type as it is written in generated source.
type mdlType struct {
	base      string // base type keyword
	ident     string // identifier reference text
	container string // "list", "set", "map"
	key       *mdlType
	elem      *mdlType
}

// mdlValue is a const value as it is written: an int literal or an
// identifier reference.
type mdlValue struct {
	intText   string
	identText string
}

// mdlDef is one top-level definition.
type mdlDef struct {
	kind   mdlKind
	name   string
	target *mdlType  // struct field type, typedef target, const type
	value  *mdlValue // struct field default, const value
	values []string  // enum member names
	extend string    // service extends reference text
}

// mdlRef is one name reference in a generated document, with the byte offset
// the renderer wrote it at.
type mdlRef struct {
	kind   store.RefKind
	text   string
	offset int
}

// mdlDoc is one generated document.
type mdlDoc struct {
	uri      uri.URI
	content  []byte
	includes []string  // include path texts, in source order
	defs     []*mdlDef // in source order
	refs     []mdlRef  // in source order

	defsByName map[string]*mdlDef
	enumValues map[string]bool
}

// mdlTarget is a resolved definition: the file, the definition's identifier
// text, and its kind.
type mdlTarget struct {
	file uri.URI
	name string
	kind mdlKind
}

// mdlHit is one reference hit in model coordinates.
type mdlHit struct {
	file  uri.URI
	start int
	end   int
	text  string
	kind  store.RefKind
}

// resolveModel is the independent resolver over a generated workspace.
type resolveModel struct {
	docs map[uri.URI]*mdlDoc
	uris []uri.URI

	incFiles  map[uri.URI][]uri.URI
	includers map[uri.URI][]uri.URI
}

// mdRender renders one document, recording each name reference's offset as
// it writes the text.
type mdlRenderer struct {
	buf  []byte
	refs []mdlRef
}

func (r *mdlRenderer) write(s string) { r.buf = append(r.buf, s...) }

func (r *mdlRenderer) ref(kind store.RefKind, text string) {
	r.refs = append(r.refs, mdlRef{kind: kind, text: text, offset: len(r.buf)})
	r.write(text)
}

// writeType renders a type, recording container element references in the
// order the source spells them.
func (r *mdlRenderer) writeType(kind store.RefKind, t *mdlType) {
	if t == nil {
		r.write("i32")

		return
	}

	switch {
	case t.base != "":
		r.write(t.base)
	case t.ident != "":
		r.ref(kind, t.ident)
	case t.container == "map":
		r.write("map<")
		r.writeType(kind, t.key)
		r.write(", ")
		r.writeType(kind, t.elem)
		r.write(">")
	case t.container == "list":
		r.write("list<")
		r.writeType(kind, t.elem)
		r.write(">")
	case t.container == "set":
		r.write("set<")
		r.writeType(kind, t.elem)
		r.write(">")
	}
}

// writeValue renders a const value. The boolean literals true/false are
// value identifiers syntactically but not references, so they are written
// without recording one.
func (r *mdlRenderer) writeValue(v *mdlValue) {
	if v == nil {
		r.write("1")

		return
	}

	if v.intText != "" {
		r.write(v.intText)

		return
	}

	r.ref(store.RefConstValue, v.identText)
}

// writeDef renders one definition.
func (r *mdlRenderer) writeDef(def *mdlDef) {
	switch def.kind {
	case mdlStruct, mdlUnion, mdlException:
		r.write(def.kind.String() + " " + def.name + " {\n  1: ")
		r.writeType(store.RefFieldType, def.target)
		r.write(" a")

		if def.value != nil {
			r.write(" = ")
			r.writeValue(def.value)
		}

		r.write(",\n}\n")
	case mdlEnum:
		r.write("enum " + def.name + " { ")

		for i, v := range def.values {
			if i > 0 {
				r.write(", ")
			}

			r.write(v + " = " + strconv.Itoa(i+1))
		}

		r.write(" }\n")
	case mdlTypedef:
		r.write("typedef ")
		r.writeType(store.RefFieldType, def.target)
		r.write(" " + def.name + "\n")
	case mdlConst:
		r.write("const ")
		r.writeType(store.RefFieldType, def.target)
		r.write(" " + def.name + " = ")
		r.writeValue(def.value)
		r.write("\n")
	case mdlService:
		r.write("service " + def.name)

		if def.extend != "" {
			r.write(" extends ")
			r.ref(store.RefServiceExtends, def.extend)
		}

		r.write(" {\n  void ping()\n}\n")
	case mdlEnumValue:
		// Enum members are not rendered on their own; they belong to an enum.
	}
}

// newResolveModel derives the include graph and the per-file name tables from
// a generated document set.
func newResolveModel(docs []*mdlDoc) *resolveModel {
	m := &resolveModel{
		docs:      make(map[uri.URI]*mdlDoc, len(docs)),
		incFiles:  make(map[uri.URI][]uri.URI, len(docs)),
		includers: make(map[uri.URI][]uri.URI),
	}

	for _, d := range docs {
		m.docs[d.uri] = d
		m.uris = append(m.uris, d.uri)

		d.defsByName = make(map[string]*mdlDef, len(d.defs))
		d.enumValues = make(map[string]bool)

		for _, def := range d.defs {
			if def.kind == mdlEnum {
				for _, v := range def.values {
					d.enumValues[v] = true
				}
			}

			d.defsByName[def.name] = def
		}
	}

	slices.Sort(m.uris)

	for _, d := range docs {
		// Include edges are deduped by resolved URI and sorted, matching the
		// view's edge table.
		for _, p := range d.includes {
			inc := mdlResolveInclude(p)
			if slices.Contains(m.incFiles[d.uri], inc) {
				continue
			}

			m.incFiles[d.uri] = append(m.incFiles[d.uri], inc)
		}

		slices.Sort(m.incFiles[d.uri])

		for _, inc := range m.incFiles[d.uri] {
			m.includers[inc] = append(m.includers[inc], d.uri)
		}
	}

	return m
}

// mdlResolveInclude resolves an include path text from a file in mdlRoot:
// every generated file lives in one directory, so the include resolves to
// the sibling of the same name whether or not that file exists.
func mdlResolveInclude(includePath string) uri.URI {
	return uri.File(path.Join(mdlRoot, includePath))
}

// mdlIncludeName derives an include's name from its path: the base name
// without its extension ("shared.thrift" -> "shared").
func mdlIncludeName(includePath string) string {
	base := path.Base(mdlResolveInclude(includePath).Path())
	if i := strings.LastIndexByte(base, '.'); i >= 0 {
		return base[:i]
	}

	return base
}

// mdlIncludeNameOf derives the include name of a file URI.
func mdlIncludeNameOf(u uri.URI) string {
	base := path.Base(u.Path())
	if i := strings.LastIndexByte(base, '.'); i >= 0 {
		return base[:i]
	}

	return base
}

// mdlBare strips the qualifier from a name: "base.User" -> "User".
func mdlBare(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}

	return name
}

// parseIdent reimplements the documented identifier split: the identifier is
// either bare, or "include.identifier" where include is one of the file's
// include names, longest first; document order breaks ties.
func (m *resolveModel) parseIdent(d *mdlDoc, text string) (include, ident string) {
	names := make([]string, 0, len(d.includes))
	for _, p := range d.includes {
		names = append(names, mdlIncludeName(p))
	}

	slices.SortStableFunc(names, func(a, b string) int { return len(b) - len(a) })

	for _, name := range names {
		if after, ok := strings.CutPrefix(text, name+"."); ok {
			return name, after
		}
	}

	return "", text
}

// includePathOf returns the include path text bound to an include name, the
// first one in document order.
func (d *mdlDoc) includePathOf(name string) string {
	for _, p := range d.includes {
		if mdlIncludeName(p) == name {
			return p
		}
	}

	return ""
}

// definitionFiles returns the files a name is resolved in, in order: the one
// included file for a qualified name, else the file itself followed by every
// transitively included file in DFS pre-order.
func (m *resolveModel) definitionFiles(d *mdlDoc, text string) []*mdlDoc {
	if include, _ := m.parseIdent(d, text); include != "" {
		p := d.includePathOf(include)
		if p == "" {
			// No include path answers to that name: treat it as local.
			return []*mdlDoc{d}
		}

		if g := m.docs[mdlResolveInclude(p)]; g != nil {
			return []*mdlDoc{g}
		}

		// The include resolves to a file that does not exist; it defines
		// nothing.
		return nil
	}

	out := []*mdlDoc{d}
	seen := map[uri.URI]bool{d.uri: true}

	var visit func(x *mdlDoc)

	visit = func(x *mdlDoc) {
		for _, p := range x.includes {
			u := mdlResolveInclude(p)
			if seen[u] {
				continue
			}

			seen[u] = true

			g := m.docs[u]
			if g == nil {
				continue
			}

			out = append(out, g)
			visit(g)
		}
	}

	visit(d)

	return out
}

// resolveType models ResolveType: base types and unresolvable names are
// unresolved; a name resolves to the first struct, union, exception, enum, or
// typedef of that name in the definition files. Other definition kinds in the
// way do not stop the search.
func (m *resolveModel) resolveType(d *mdlDoc, text string) (mdlTarget, bool) {
	if text == "" || mdlBasicTypes[text] {
		return mdlTarget{}, false
	}

	_, ident := m.parseIdent(d, text)

	for _, g := range m.definitionFiles(d, text) {
		def, ok := g.defsByName[ident]
		if !ok {
			continue
		}

		switch def.kind {
		case mdlStruct, mdlUnion, mdlException, mdlEnum, mdlTypedef:
			return mdlTarget{file: g.uri, name: def.name, kind: def.kind}, true
		default:
		}
	}

	return mdlTarget{}, false
}

// resolveValue models ResolveValue: the identifier's bare name is looked up in
// each definition file, enum values first, then consts.
func (m *resolveModel) resolveValue(d *mdlDoc, text string) (mdlTarget, bool) {
	if text == "true" || text == "false" {
		return mdlTarget{}, false
	}

	_, ident := m.parseIdent(d, text)
	ident = mdlBare(ident)

	for _, g := range m.definitionFiles(d, text) {
		if g.enumValues[ident] {
			return mdlTarget{file: g.uri, name: ident, kind: mdlEnumValue}, true
		}

		if def, ok := g.defsByName[ident]; ok && def.kind == mdlConst {
			return mdlTarget{file: g.uri, name: def.name, kind: mdlConst}, true
		}
	}

	return mdlTarget{}, false
}

// resolveService models ResolveService: the name resolves to the first service
// of that name in the definition files.
func (m *resolveModel) resolveService(d *mdlDoc, text string) (mdlTarget, bool) {
	_, ident := m.parseIdent(d, text)

	for _, g := range m.definitionFiles(d, text) {
		if def, ok := g.defsByName[ident]; ok && def.kind == mdlService {
			return mdlTarget{file: g.uri, name: def.name, kind: mdlService}, true
		}
	}

	return mdlTarget{}, false
}

// resolveReference models one reference hop: the resolution a reference in a
// given grammar slot submits to.
func (m *resolveModel) resolveReference(d *mdlDoc, r mdlRef) (mdlTarget, bool) {
	switch r.kind {
	case store.RefFieldType, store.RefSignatureType, store.RefAnnotationType:
		return m.resolveType(d, r.text)
	case store.RefConstValue:
		return m.resolveValue(d, r.text)
	case store.RefServiceExtends:
		return m.resolveService(d, r.text)
	}

	return mdlTarget{}, false
}

// dependents returns every file that transitively includes f, sorted. A file
// that includes itself through a cycle appears in its own dependents.
func (m *resolveModel) dependents(f uri.URI) []uri.URI {
	out := make([]uri.URI, 0)
	seen := make(map[uri.URI]bool)
	queue := []uri.URI{f}

	for head := 0; head < len(queue); head++ {
		for _, dep := range m.includers[queue[head]] {
			if seen[dep] {
				continue
			}

			seen[dep] = true
			out = append(out, dep)
			queue = append(queue, dep)
		}
	}

	slices.Sort(out)

	return out
}

// searchFiles returns f followed by its transitive dependents, f excluded.
func (m *resolveModel) searchFiles(f uri.URI) []*mdlDoc {
	out := make([]*mdlDoc, 0, 1+len(m.includers))

	if d := m.docs[f]; d != nil {
		out = append(out, d)
	}

	for _, dep := range m.dependents(f) {
		if dep == f {
			continue
		}

		if d := m.docs[dep]; d != nil {
			out = append(out, d)
		}
	}

	return out
}

// mdlKindAllowed reports whether k is in kinds; an empty kinds matches all.
func mdlKindAllowed(k store.RefKind, kinds []store.RefKind) bool {
	return len(kinds) == 0 || slices.Contains(kinds, k)
}

// mdlEnumSegment splits a value identifier on dots and returns the first
// segment equal to enumName with its byte offset.
func mdlEnumSegment(text, enumName string) (seg string, off int, ok bool) {
	off = 0

	for _, item := range strings.Split(text, ".") {
		if item == enumName {
			return item, off, true
		}

		off += len(item) + 1
	}

	return "", 0, false
}

// referencesTo models Index.ReferencesTo over the model workspace.
func (m *resolveModel) referencesTo(def mdlTarget, kinds []store.RefKind) []mdlHit {
	var out []mdlHit

	enumName := ""
	if def.kind == mdlEnum {
		enumName = def.name
	}

	for _, d := range m.searchFiles(def.file) {
		for _, r := range d.refs {
			if r.kind == store.RefConstValue && enumName != "" && mdlKindAllowed(store.RefConstValue, kinds) {
				seg, off, ok := mdlEnumSegment(r.text, enumName)
				if !ok {
					continue
				}

				qualifier := r.text[:off+len(seg)]

				enum, ok := m.resolveType(d, qualifier)
				if !ok || enum != def {
					continue
				}

				out = append(out, mdlHit{
					file: d.uri, start: r.offset + off, end: r.offset + off + len(seg),
					text: seg, kind: store.RefConstValue,
				})

				continue
			}

			if !mdlKindAllowed(r.kind, kinds) {
				continue
			}

			if mdlBare(r.text) != mdlBare(def.name) {
				continue
			}

			resolved, ok := m.resolveReference(d, r)
			if !ok || resolved != def {
				continue
			}

			out = append(out, mdlHit{
				file: d.uri, start: r.offset, end: r.offset + len(r.text), text: r.text, kind: r.kind,
			})
		}
	}

	return out
}

// references models Index.References: name-matched, resolution-blind.
func (m *resolveModel) references(f uri.URI, name string, kinds []store.RefKind) []mdlHit {
	var out []mdlHit

	for _, d := range m.searchFiles(f) {
		for _, r := range d.refs {
			if !mdlKindAllowed(r.kind, kinds) {
				continue
			}

			if mdlBare(r.text) != mdlBare(name) {
				continue
			}

			out = append(out, mdlHit{
				file: d.uri, start: r.offset, end: r.offset + len(r.text), text: r.text, kind: r.kind,
			})
		}
	}

	return out
}

// findInWorkspace models Index.FindInWorkspace: the first known file (sorted)
// defining the name, else the first file of a sorted directory walk. A
// qualified query keeps only files whose include name matches the qualifier.
func (m *resolveModel) findInWorkspace(name string, knownFiles []uri.URI) (mdlTarget, bool) {
	include, ident := mdlSplitQualified(name)

	lookup := func(f uri.URI) (mdlTarget, bool) {
		if include != "" && mdlIncludeNameOf(f) != include {
			return mdlTarget{}, false
		}

		d := m.docs[f]
		if d == nil {
			return mdlTarget{}, false
		}

		def, ok := d.defsByName[ident]
		if !ok {
			return mdlTarget{}, false
		}

		return mdlTarget{file: f, name: def.name, kind: def.kind}, true
	}

	for _, f := range knownFiles {
		if t, ok := lookup(f); ok {
			return t, true
		}
	}

	for _, f := range m.uris {
		if !strings.HasSuffix(f.Path(), ".thrift") {
			continue
		}

		if t, ok := lookup(f); ok {
			return t, true
		}
	}

	return mdlTarget{}, false
}

// mdlSplitQualified splits at the last dot, matching the qualified-name split
// FindInWorkspace documents.
func mdlSplitQualified(name string) (include, ident string) {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[:i], name[i+1:]
	}

	return "", name
}

// mdlBasicTypes are the built-in base types, which never resolve to a
// definition.
var mdlBasicTypes = map[string]bool{
	"bool": true, "byte": true, "i8": true, "i16": true, "i32": true,
	"i64": true, "double": true, "string": true, "binary": true,
	"slist": true, "uuid": true,
}

// underlyingType models UnderlyingType: follow the typedef chain, file by
// file, until a type that is not a typedef is reached. An identifier that
// resolves to nothing, and a chain that never terminates within the depth
// cap, both have no classifiable kind.
func (m *resolveModel) underlyingType(from *mdlDoc, t *mdlType) (*mdlType, *mdlDoc) {
	pf, cur := from, t

	for range mdlMaxTypedefDepth {
		if cur == nil || cur.ident == "" {
			return cur, pf
		}

		target, ok := m.resolveType(pf, cur.ident)
		if !ok || target.kind != mdlTypedef {
			if !ok {
				return nil, pf
			}

			return cur, pf
		}

		def := m.docs[target.file].defsByName[target.name]
		pf, cur = m.docs[target.file], def.target
	}

	return nil, pf
}

// mdlMaxTypedefDepth is the documented typedef-chain depth cap.
const mdlMaxTypedefDepth = 32

// mdlWorkspace is one generated workspace plus its model.
type mdlWorkspace struct {
	model *resolveModel
	files map[uri.URI][]byte
}

// files returns the content map of the model's documents.
func (w *mdlWorkspace) contents() map[uri.URI][]byte {
	out := make(map[uri.URI][]byte, len(w.model.docs))
	for u, d := range w.model.docs {
		out[u] = d.content
	}

	return out
}

// mdlDrawWorkspace draws a generated workspace: the file set, its model, and
// the rendered content.
func mdlDrawWorkspace(t *proptest.T) *mdlWorkspace {
	n := proptest.IntRange(2, 5).Draw(t)

	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("f%d", i)
	}

	docs := make([]*mdlDoc, 0, n)
	byURI := make(map[uri.URI]*mdlDoc, n)

	for i, name := range names {
		doc := &mdlDoc{uri: uri.File(mdlRoot + "/" + name + ".thrift")}

		for range proptest.IntRange(0, 3).Draw(t) {
			doc.includes = append(doc.includes, mdlDrawIncludePath(t, names))
		}

		// Names are distinct within one file, so a file's definition table is
		// unambiguous; the three-name pool still collides constantly across
		// files.
		defCount := proptest.IntRange(0, 3).Draw(t)
		picked := proptest.SliceOfNDistinct(
			proptest.SampledFrom(mdlNames), defCount, defCount, func(s string) string { return s },
		).Draw(t)

		for _, name := range picked {
			doc.defs = append(doc.defs, mdlDrawDef(t, name, names))
		}

		// Probe structs carry references without introducing pool names.
		for j := range proptest.IntRange(0, 2).Draw(t) {
			doc.defs = append(doc.defs, mdlDrawProbe(t, fmt.Sprintf("P%d_%d", i, j), names))
		}

		// A typedef chain, so chains that end at a base type, at a container,
		// on an unresolvable name, and on themselves are present in every
		// workspace.
		doc.defs = append(doc.defs, mdlDrawChain(t, names)...)

		docs = append(docs, doc)
		byURI[doc.uri] = doc
	}

	// Enum-qualified value references: one in the enum's own scope, so the
	// qualifier resolves to the enum, and one from a file that may reach
	// it through an include.
	mdlAddEnumRefs(t, docs, byURI)

	for _, doc := range docs {
		r := &mdlRenderer{}

		for _, p := range doc.includes {
			r.write("include \"" + p + "\"\n")
		}

		for _, def := range doc.defs {
			r.writeDef(def)
		}

		doc.content = r.buf
		doc.refs = r.refs
	}

	w := &mdlWorkspace{model: newResolveModel(docs)}
	w.files = w.contents()

	return w
}

// mdlAddEnumRefs appends consts whose values are enum-qualified references:
// in the enum's own file (a qualifier that resolves to the enum, unless
// the name is shadowed by an earlier definition of the same name), through a
// direct include, in the invalid dotted-filename spelling, and behind a
// qualifier that names no include at all.
func mdlAddEnumRefs(t *proptest.T, docs []*mdlDoc, byURI map[uri.URI]*mdlDoc) {
	extra := make(map[uri.URI][]*mdlDef, len(docs))

	for _, doc := range docs {
		type candidate struct {
			def         *mdlDef
			includeName string // "" when the enum lives in this file
		}

		var candidates []candidate

		for _, def := range doc.defs {
			if def.kind == mdlEnum && len(def.values) > 0 {
				candidates = append(candidates, candidate{def: def})
			}
		}

		for _, p := range doc.includes {
			g := byURI[mdlResolveInclude(p)]
			if g == nil {
				continue
			}

			for _, def := range g.defs {
				if def.kind == mdlEnum && len(def.values) > 0 && g.uri != doc.uri {
					candidates = append(candidates, candidate{def: def, includeName: mdlIncludeName(p)})
				}
			}
		}

		// An enum-qualified reference from a file that reaches the enum: the
		// most interesting case for reference search, so it is always present
		// when the workspace has an enum in scope.
		for j := range proptest.IntRange(0, 2).Draw(t) {
			if len(candidates) == 0 {
				break
			}

			c := candidates[proptest.IntRange(0, len(candidates)-1).Draw(t)]
			member := c.def.values[proptest.IntRange(0, len(c.def.values)-1).Draw(t)]

			var text string

			switch proptest.IntRange(0, 5).Draw(t) {
			case 0, 1, 2:
				text = c.def.name + "." + member
			case 3:
				text = mdlQualify(c.includeName, c.def.name) + "." + member
			case 4:
				text = mdlQualify(c.includeName, c.def.name+".thrift") + "." + member
			default:
				text = "Nope." + c.def.name + "." + member
			}

			extra[doc.uri] = append(extra[doc.uri], &mdlDef{
				kind:   mdlConst,
				name:   fmt.Sprintf("Q%d", j),
				target: &mdlType{base: "i32"},
				value:  &mdlValue{identText: text},
			})
		}
	}

	// The same reference in the enum's own file, where the qualifier resolves
	// to that enum directly.
	for _, doc := range docs {
		for _, def := range doc.defs {
			if def.kind != mdlEnum || len(def.values) == 0 || !proptest.Bool().Draw(t) {
				continue
			}

			member := def.values[proptest.IntRange(0, len(def.values)-1).Draw(t)]

			text := def.name + "." + member
			if proptest.Bool().Draw(t) {
				text = def.name + ".thrift." + member
			}

			extra[doc.uri] = append(extra[doc.uri], &mdlDef{
				kind:   mdlConst,
				name:   fmt.Sprintf("R%d", len(extra[doc.uri])),
				target: &mdlType{base: "i32"},
				value:  &mdlValue{identText: text},
			})
		}
	}

	for _, doc := range docs {
		doc.defs = append(doc.defs, extra[doc.uri]...)
	}
}

// mdlQualify joins an include name with a name, or returns the name alone when
// no include qualifies it.
func mdlQualify(includeName, name string) string {
	if includeName == "" {
		return name
	}

	return includeName + "." + name
}

// mdlDrawIncludePath draws one include path text: an existing file (possibly
// the same file, possibly spelled "./x.thrift"), or a file that does not
// exist.
func mdlDrawIncludePath(t *proptest.T, names []string) string {
	pick := proptest.IntRange(0, 5).Draw(t)
	target := names[proptest.IntRange(0, len(names)-1).Draw(t)]

	switch pick {
	case 0, 1:
		return target + ".thrift"
	case 2, 3:
		return "./" + target + ".thrift"
	case 4:
		return "ghost" + strconv.Itoa(proptest.IntRange(0, 2).Draw(t)) + ".thrift"
	}

	return target + ".thrift"
}

// mdlDrawType draws a type reference: a base type, a bare name, a qualified
// reference, the invalid dotted-filename spelling, a missing name, or a
// container of one of those.
func mdlDrawType(t *proptest.T, depth int, names []string) *mdlType {
	pick := proptest.IntRange(0, 10).Draw(t)
	file := func() string { return names[proptest.IntRange(0, len(names)-1).Draw(t)] }
	name := func() string { return mdlNames[proptest.IntRange(0, len(mdlNames)-1).Draw(t)] }

	switch pick {
	case 0:
		return &mdlType{base: "i32"}
	case 1:
		return &mdlType{base: "string"}
	case 2:
		return &mdlType{base: "bool"}
	case 3:
		return &mdlType{ident: name()}
	case 4:
		return &mdlType{ident: file() + "." + name()}
	case 5:
		// The dotted-filename spelling upstream Thrift does not accept.
		return &mdlType{ident: file() + ".thrift." + name()}
	case 6:
		return &mdlType{ident: "Ghost"}
	case 7:
		return &mdlType{ident: file() + ".Ghost"}
	}

	if depth >= 2 {
		return &mdlType{base: "i32"}
	}

	switch pick {
	case 8:
		return &mdlType{container: "list", elem: mdlDrawType(t, depth+1, names)}
	case 9:
		return &mdlType{container: "set", elem: mdlDrawType(t, depth+1, names)}
	}

	return &mdlType{container: "map", key: &mdlType{base: "i32"}, elem: mdlDrawType(t, depth+1, names)}
}

// mdlDrawValue draws a const value: an int literal, a boolean literal, or an
// identifier reference (bare, qualified, enum-qualified, and the
// dotted-filename spelling).
func mdlDrawValue(t *proptest.T, names []string) *mdlValue {
	pick := proptest.IntRange(0, 9).Draw(t)
	file := func() string { return names[proptest.IntRange(0, len(names)-1).Draw(t)] }
	name := func() string { return mdlNames[proptest.IntRange(0, len(mdlNames)-1).Draw(t)] }
	enumValue := func() string {
		return mdlEnumValues[proptest.IntRange(0, len(mdlEnumValues)-1).Draw(t)]
	}

	switch pick {
	case 0:
		return &mdlValue{intText: "1"}
	case 1:
		return &mdlValue{intText: "0x10"}
	case 2:
		return &mdlValue{intText: "true"}
	case 3:
		return &mdlValue{identText: enumValue()}
	case 4:
		return &mdlValue{identText: file() + "." + enumValue()}
	case 5:
		return &mdlValue{identText: name() + "." + enumValue()}
	case 6:
		return &mdlValue{identText: file() + "." + name() + "." + enumValue()}
	case 7:
		return &mdlValue{identText: name()}
	case 8:
		return &mdlValue{identText: file() + "." + name()}
	}

	return &mdlValue{identText: "Ghost"}
}

// mdlDrawDef draws one definition of a given name.
func mdlDrawDef(t *proptest.T, name string, names []string) *mdlDef {
	pick := proptest.IntRange(0, 6).Draw(t)

	switch pick {
	case 0:
		return &mdlDef{kind: mdlStruct, name: name, target: mdlDrawType(t, 0, names), value: mdlDrawOptionalValue(t, names)}
	case 1:
		return &mdlDef{kind: mdlUnion, name: name, target: mdlDrawType(t, 0, names)}
	case 2:
		return &mdlDef{kind: mdlException, name: name, target: mdlDrawType(t, 0, names)}
	case 3:
		count := proptest.IntRange(1, 2).Draw(t)
		values := proptest.SliceOfNDistinct(
			proptest.SampledFrom(mdlEnumValues), count, count, func(s string) string { return s },
		).Draw(t)

		return &mdlDef{kind: mdlEnum, name: name, values: values}
	case 4:
		return &mdlDef{kind: mdlTypedef, name: name, target: mdlDrawType(t, 0, names)}
	case 5:
		return &mdlDef{kind: mdlConst, name: name, target: mdlDrawType(t, 0, names), value: mdlDrawValue(t, names)}
	}

	def := &mdlDef{kind: mdlService, name: name}

	switch proptest.IntRange(0, 3).Draw(t) {
	case 0:
	case 1:
		def.extend = mdlNames[proptest.IntRange(0, len(mdlNames)-1).Draw(t)]
	case 2:
		def.extend = names[proptest.IntRange(0, len(names)-1).Draw(t)] +
			"." + mdlNames[proptest.IntRange(0, len(mdlNames)-1).Draw(t)]
	case 3:
		def.extend = names[proptest.IntRange(0, len(names)-1).Draw(t)] +
			".thrift." + mdlNames[proptest.IntRange(0, len(mdlNames)-1).Draw(t)]
	}

	return def
}

// mdlDrawProbe draws a struct whose fields exist only to carry references.
func mdlDrawProbe(t *proptest.T, name string, names []string) *mdlDef {
	return &mdlDef{kind: mdlStruct, name: name, target: mdlDrawType(t, 0, names), value: mdlDrawOptionalValue(t, names)}
}

// mdlDrawChain draws a typedef chain: each link is a base type, a container, a
// further alias in the same file, an alias reached through an include, or an
// unresolvable name; the last link may close the chain into a cycle. Chain
// names are drawn from their own pool, so a chain never collides with the
// definition-name pool.
func mdlDrawChain(t *proptest.T, names []string) []*mdlDef {
	length := proptest.IntRange(1, 3).Draw(t)

	links := make([]string, length)
	for i := range links {
		links[i] = fmt.Sprintf("C%d", i)
	}

	defs := make([]*mdlDef, 0, length)

	for i := range length {
		def := &mdlDef{kind: mdlTypedef, name: links[i]}
		last := i+1 == length

		switch proptest.IntRange(0, 5).Draw(t) {
		case 0:
			def.target = &mdlType{base: "i32"}
		case 1:
			def.target = &mdlType{base: "string"}
		case 2:
			def.target = &mdlType{container: "list", elem: &mdlType{base: "i32"}}
		case 3:
			if !last {
				def.target = &mdlType{ident: links[i+1]}
			} else {
				def.target = &mdlType{ident: links[0]} // closes the chain into a cycle
			}
		case 4:
			file := names[proptest.IntRange(0, len(names)-1).Draw(t)]
			if !last {
				def.target = &mdlType{ident: file + "." + links[i+1]}
			} else {
				def.target = &mdlType{ident: file + "." + links[0]}
			}
		default:
			def.target = &mdlType{ident: "Ghost"}
		}

		defs = append(defs, def)
	}

	return defs
}

// mdlDrawOptionalValue draws a field default half the time.
func mdlDrawOptionalValue(t *proptest.T, names []string) *mdlValue {
	if !proptest.Bool().Draw(t) {
		return nil
	}

	return mdlDrawValue(t, names)
}

// mdlRoot is the directory every generated workspace lives in.
const mdlRoot = "/w"

// mdlNewView builds an in-memory view over files, optionally parsing them.
func mdlNewView(files map[uri.URI][]byte, parseAll bool) *store.View {
	v := store.NewView(uri.File(mdlRoot), store.NewOverlayFS(store.NewMemFS(files)), nil)
	if !parseAll {
		return v
	}

	for _, u := range slices.Sorted(maps.Keys(files)) {
		_, _ = v.Parse(context.Background(), u)
	}

	return v
}

// mdlFieldType wraps a name in the identifier type the index resolves.
func mdlFieldType(text string) *syntax.FieldType {
	return &syntax.FieldType{Kind: syntax.TypeIdent, Ident: &syntax.Identifier{Text: text}}
}

// mdlQueryKind is the resolver a query submits to.
type mdlQueryKind uint8

const (
	mdlQueryType mdlQueryKind = iota + 1
	mdlQueryValue
	mdlQueryService
)

// mdlQuery is one resolution question.
type mdlQuery struct {
	file uri.URI
	kind mdlQueryKind
	text string
}

// mdlAnswer is a resolution answer in comparable form.
type mdlAnswer struct {
	ok   bool
	file uri.URI
	name string
	kind sema.DefinitionKind
}

// mdlAsk asks production one question through a fresh index.
func mdlAsk(ctx context.Context, view *store.View, pf *store.ParsedFile, q mdlQuery) (mdlAnswer, error) {
	ix := sema.NewIndex(view)

	var (
		def *sema.Resolved
		err error
	)

	switch q.kind {
	case mdlQueryType:
		def, err = ix.ResolveType(ctx, pf, mdlFieldType(q.text))
	case mdlQueryValue:
		def, err = ix.ResolveValue(ctx, pf, &syntax.ConstValue{Kind: syntax.ValueIdent, Text: q.text})
	case mdlQueryService:
		def, err = ix.ResolveService(ctx, pf, &syntax.Identifier{Text: q.text})
	}

	if err != nil || def == nil {
		return mdlAnswer{}, err
	}

	return mdlAnswer{ok: true, file: def.File, name: def.Name.Text, kind: def.Kind}, nil
}

// mdlAnswerOf model-answers one question.
func (m *resolveModel) answer(d *mdlDoc, q mdlQuery) mdlAnswer {
	var (
		target mdlTarget
		ok     bool
	)

	switch q.kind {
	case mdlQueryType:
		target, ok = m.resolveType(d, q.text)
	case mdlQueryValue:
		target, ok = m.resolveValue(d, q.text)
	case mdlQueryService:
		target, ok = m.resolveService(d, q.text)
	}

	if !ok {
		return mdlAnswer{}
	}

	return mdlAnswer{ok: true, file: target.file, name: target.name, kind: target.kind.semaKind()}
}

// mdlQueries lists the questions asked of one generated document: every
// reference it contains, plus every pool name in every resolver slot and every
// qualified spelling.
func (m *resolveModel) queries(d *mdlDoc) []mdlQuery {
	var out []mdlQuery

	for _, r := range d.refs {
		switch r.kind {
		case store.RefFieldType, store.RefSignatureType, store.RefAnnotationType:
			out = append(out, mdlQuery{file: d.uri, kind: mdlQueryType, text: r.text})
		case store.RefConstValue:
			out = append(out, mdlQuery{file: d.uri, kind: mdlQueryValue, text: r.text})
		case store.RefServiceExtends:
			out = append(out, mdlQuery{file: d.uri, kind: mdlQueryService, text: r.text})
		}
	}

	for _, name := range mdlNames {
		out = append(out,
			mdlQuery{file: d.uri, kind: mdlQueryType, text: name},
			mdlQuery{file: d.uri, kind: mdlQueryValue, text: name},
			mdlQuery{file: d.uri, kind: mdlQueryService, text: name},
		)

		for _, f := range m.uris {
			inc := mdlIncludeNameOf(f)
			out = append(out,
				mdlQuery{file: d.uri, kind: mdlQueryType, text: inc + "." + name},
				mdlQuery{file: d.uri, kind: mdlQueryType, text: inc + ".thrift." + name},
			)
		}
	}

	return out
}

// mdlDefTargets lists every definition in the workspace as a resolution
// target, enum members included.
func (m *resolveModel) defTargets() []mdlTarget {
	var out []mdlTarget

	for _, u := range m.uris {
		d := m.docs[u]

		for _, def := range d.defs {
			out = append(out, mdlTarget{file: d.uri, name: def.name, kind: def.kind})

			for _, v := range def.values {
				out = append(out, mdlTarget{file: d.uri, name: v, kind: mdlEnumValue})
			}
		}
	}

	return out
}

// mdlResolveDef asks production for the Resolved of a model target, from the
// defining file's own scope.
func mdlResolveDef(ctx context.Context, view *store.View, target mdlTarget) (*sema.Resolved, error) {
	pf, err := view.Parse(ctx, target.file)
	if err != nil {
		return nil, err
	}

	ix := sema.NewIndex(view)

	switch target.kind {
	case mdlConst, mdlEnumValue:
		return ix.ResolveValue(ctx, pf, &syntax.ConstValue{Kind: syntax.ValueIdent, Text: target.name})
	case mdlService:
		return ix.ResolveService(ctx, pf, &syntax.Identifier{Text: target.name})
	case mdlStruct, mdlUnion, mdlException, mdlEnum, mdlTypedef:
		return ix.ResolveType(ctx, pf, mdlFieldType(target.name))
	}

	return nil, nil
}

// mdlResolveHitAgain re-resolves one hit through a second, independent index:
// it finds the reference the hit covers in the hit's file and submits it to the
// resolver its grammar slot belongs to.
func mdlResolveHitAgain(ctx context.Context, view *store.View, h mdlHit) *sema.Resolved {
	pf, err := view.Parse(ctx, h.file)
	if err != nil {
		return nil
	}

	var node syntax.Node

	for _, r := range pf.Index().References() {
		start, _ := pf.AST().Range(r.Node)
		if start.Offset == h.start && r.Name == h.text && r.Kind == h.kind {
			node = r.Node

			break
		}
	}

	if node == nil {
		return nil
	}

	ix := sema.NewIndex(view)

	var def *sema.Resolved

	switch h.kind {
	case store.RefFieldType, store.RefSignatureType, store.RefAnnotationType:
		ident, ok := node.(*syntax.Identifier)
		if !ok {
			return nil
		}

		def, _ = ix.ResolveType(ctx, pf, mdlFieldType(ident.Text))
	case store.RefConstValue:
		value, ok := node.(*syntax.ConstValue)
		if !ok {
			return nil
		}

		def, _ = ix.ResolveValue(ctx, pf, value)
	case store.RefServiceExtends:
		ident, ok := node.(*syntax.Identifier)
		if !ok {
			return nil
		}

		def, _ = ix.ResolveService(ctx, pf, ident)
	}

	return def
}

// mdlProductionHits converts production hits into comparable model hits.
func mdlProductionHits(hits []sema.Hit) []mdlHit {
	if len(hits) == 0 {
		return nil
	}

	out := make([]mdlHit, len(hits))
	for i, h := range hits {
		out[i] = mdlHit{
			file:  h.File,
			start: h.Span.Start.Offset,
			end:   h.Span.End.Offset,
			text:  h.Text,
			kind:  h.Kind,
		}
	}

	return out
}

// mdlDescOf renders a production FieldType for comparison.
func mdlDescOf(ft *syntax.FieldType) string {
	if ft == nil {
		return "<nil>"
	}

	switch ft.Kind {
	case syntax.TypeIdent:
		if ft.Ident == nil {
			return "ident(?)"
		}

		return "ident(" + ft.Ident.Text + ")"
	case syntax.TypeBase:
		return "base(" + ft.Base.String() + ")"
	case syntax.TypeMap:
		return "map"
	case syntax.TypeList:
		return "list"
	case syntax.TypeSet:
		return "set"
	}

	return fmt.Sprintf("kind(%d)", ft.Kind)
}

// mdlModelDesc renders a model type for comparison, in the same form as
// mdlDescOf.
func mdlModelDesc(t *mdlType) string {
	if t == nil {
		return "<nil>"
	}

	switch {
	case t.base != "":
		return "base(" + t.base + ")"
	case t.ident != "":
		return "ident(" + t.ident + ")"
	case t.container != "":
		return t.container
	}

	return "unknown"
}

// resolveSeeds is the corpus every resolution property in this file starts
// from. proptest.New derives its draw stream from the bytes' hash, so each entry
// starts one independent stream per target, and the fuzzer varies the bytes
// from there.
var resolveSeeds = proptest.Corpus{
	[]byte("T U N"),
	[]byte("struct T {\n  1: U u,\n}\ninclude \"f0.thrift\"\n"),
	[]byte("include \"./f1.thrift\"\ninclude \"f1.thrift\"\n"),
	[]byte("shared.Color.RED"),
	[]byte("typedef C0 C1\ntypedef C1 C0\n"),
	[]byte("ghost.thrift.Ghost"),
}

// FuzzReferenceIndexMatchesSource checks that a file's reference index holds
// its source references in order, at their written offsets.
func FuzzReferenceIndexMatchesSource(f *testing.F) {
	proptest.Fuzz(f, resolveSeeds, func(tb *testing.T, seed []byte) {
		t := proptest.New(tb, seed)

		ctx := t.Context()
		ws := mdlDrawWorkspace(t)
		view := mdlNewView(ws.files, true)

		for _, u := range ws.model.uris {
			d := ws.model.docs[u]

			pf, err := view.Parse(ctx, u)
			require.NoError(t, err)

			var got []mdlRef

			for _, r := range pf.Index().References() {
				start, _ := pf.AST().Range(r.Node)
				got = append(got, mdlRef{kind: r.Kind, text: r.Name, offset: start.Offset})
			}

			require.Equal(t, d.refs, got, "reference index of\n%s", d.content)
		}
	})
}

// resolveCoverage counts the resolution outcomes the differential must
// exercise: a generator that never resolves, or never fails to resolve, would
// make the property vacuous.
type resolveCoverage struct {
	resolvedType    int
	resolvedValue   int
	resolvedService int
	unresolved      int
}

// resolveDifferential runs the resolution differential over the stream seed
// derives from: every question the model can ask, answered by production and
// by the model. The counts say which outcomes the run covered.
func resolveDifferential(tb *testing.T, seed []byte) resolveCoverage {
	t := proptest.New(tb, seed)

	var cov resolveCoverage

	ctx := t.Context()
	ws := mdlDrawWorkspace(t)
	view := mdlNewView(ws.files, true)

	for _, u := range ws.model.uris {
		d := ws.model.docs[u]

		pf, err := view.Parse(ctx, u)
		require.NoError(t, err)
		require.Empty(t, pf.Errors(), "generated document must parse cleanly:\n%s", d.content)

		for _, q := range ws.model.queries(d) {
			want := ws.model.answer(d, q)

			if !want.ok {
				cov.unresolved++
			} else {
				switch q.kind {
				case mdlQueryType:
					cov.resolvedType++
				case mdlQueryValue:
					cov.resolvedValue++
				case mdlQueryService:
					cov.resolvedService++
				}
			}

			got, err := mdlAsk(ctx, view, pf, q)
			require.NoError(t, err)
			require.Equal(t, want, got, "resolving %q (resolver %d) from %s in\n%s",
				q.text, q.kind, u.Path(), d.content)
		}
	}

	return cov
}

// FuzzIndexResolveDifferential compares every resolution question the model
// can ask against a fresh index's answer: qualified and unqualified types,
// values (enum members and consts, bare and qualified), and services, over
// generated include graphs that cycle, self-include, repeat, and dangle.
func FuzzIndexResolveDifferential(f *testing.F) {
	proptest.Fuzz(f, resolveSeeds, func(tb *testing.T, seed []byte) {
		resolveDifferential(tb, seed)
	})
}

// TestIndexResolveDifferentialCoversResolvers pins the generator coverage the
// differential leans on, over the same seed corpus the fuzz target runs.
func TestIndexResolveDifferentialCoversResolvers(t *testing.T) {
	var total resolveCoverage

	for _, seed := range resolveSeeds {
		c := resolveDifferential(t, seed)
		total.resolvedType += c.resolvedType
		total.resolvedValue += c.resolvedValue
		total.resolvedService += c.resolvedService
		total.unresolved += c.unresolved
	}

	// A differential that never resolves anything proves nothing: the
	// generated workspaces must exercise every resolver in both directions.
	assert.Greater(t, total.resolvedType, 0, "no generated type reference ever resolved")
	assert.Greater(t, total.resolvedValue, 0, "no generated value reference ever resolved")
	assert.Greater(t, total.resolvedService, 0, "no generated service name ever resolved")
	assert.Greater(t, total.unresolved, 0, "no generated name ever failed to resolve")
}

// memoCoverage counts the (file, name) pairs whose three resolvers disagree.
// Without them the transparency property never exercises memo keys that mix
// definition kinds.
type memoCoverage struct {
	crossKind int
}

// indexMemoTransparency runs the memo-transparency property over the stream
// seed derives from. The counts say how much cross-kind material the run
// covered.
func indexMemoTransparency(tb *testing.T, seed []byte) memoCoverage {
	t := proptest.New(tb, seed)

	var cov memoCoverage

	ctx := t.Context()
	ws := mdlDrawWorkspace(t)
	view := mdlNewView(ws.files, true)

	var qs []mdlQuery

	for _, u := range ws.model.uris {
		qs = append(qs, ws.model.queries(ws.model.docs[u])...)
	}

	qs = proptest.Permutation(qs).Draw(t)

	// Count the (file, name) pairs whose three resolvers disagree: the
	// property is only meaningful if the generator produces them.
	for _, u := range ws.model.uris {
		d := ws.model.docs[u]

		for _, name := range mdlNames {
			asType := ws.model.answer(d, mdlQuery{file: u, kind: mdlQueryType, text: name})
			asValue := ws.model.answer(d, mdlQuery{file: u, kind: mdlQueryValue, text: name})
			asService := ws.model.answer(d, mdlQuery{file: u, kind: mdlQueryService, text: name})

			if asType != asValue || asType != asService || asValue != asService {
				cov.crossKind++
			}
		}
	}

	ix := sema.NewIndex(view)
	pfs := make(map[uri.URI]*store.ParsedFile, len(ws.model.docs))

	parsed := func(u uri.URI) *store.ParsedFile {
		if pf, ok := pfs[u]; ok {
			return pf
		}

		pf, err := view.Parse(ctx, u)
		require.NoError(t, err)
		pfs[u] = pf

		return pf
	}

	ask := func(q mdlQuery) mdlAnswer {
		pf := parsed(q.file)

		var (
			def *sema.Resolved
			err error
		)

		switch q.kind {
		case mdlQueryType:
			def, err = ix.ResolveType(ctx, pf, mdlFieldType(q.text))
		case mdlQueryValue:
			def, err = ix.ResolveValue(ctx, pf, &syntax.ConstValue{Kind: syntax.ValueIdent, Text: q.text})
		case mdlQueryService:
			def, err = ix.ResolveService(ctx, pf, &syntax.Identifier{Text: q.text})
		}

		require.NoError(t, err)

		if def == nil {
			return mdlAnswer{}
		}

		return mdlAnswer{ok: true, file: def.File, name: def.Name.Text, kind: def.Kind}
	}

	warm := make([]mdlAnswer, len(qs))
	for i, q := range qs {
		warm[i] = ask(q)
	}

	// The same questions again on the same index: no drift.
	for i, q := range qs {
		require.Equal(t, warm[i], ask(q), "reused index changed its answer for %q", q.text)
	}

	// A fresh index per question: the memoized answers are transparent.
	for i, q := range qs {
		fresh, err := mdlAsk(ctx, view, parsed(q.file), q)
		require.NoError(t, err)
		require.Equal(t, fresh, warm[i],
			"reused index answered %q (resolver %d) from %s differently than a fresh index",
			q.text, q.kind, q.file.Path())
	}

	return cov
}

// FuzzIndexMemoTransparency checks that repeated questions answered through a
// memoizing Index match fresh-Index answers, including unresolved results and
// the same name resolved as different kinds.
func FuzzIndexMemoTransparency(f *testing.F) {
	proptest.Fuzz(f, resolveSeeds, func(tb *testing.T, seed []byte) {
		indexMemoTransparency(tb, seed)
	})
}

// TestIndexMemoTransparencyCoversCrossKindNames pins the generator coverage
// the transparency property leans on.
func TestIndexMemoTransparencyCoversCrossKindNames(t *testing.T) {
	var total memoCoverage

	for _, seed := range resolveSeeds {
		c := indexMemoTransparency(t, seed)
		total.crossKind += c.crossKind
	}

	assert.Greater(t, total.crossKind, 0,
		"the generator must produce names resolved as different kinds in one file; the memo-transparency property is vacuous without them")
}

// TestIndexMemoDoesNotMixKinds is the deterministic companion to the
// transparency property: one name, a struct in one file and a const in
// another, resolved as a type and as a value from the same including file in
// both possible orders.
func TestIndexMemoDoesNotMixKinds(t *testing.T) {
	ctx := t.Context()

	view := mdlNewView(map[uri.URI][]byte{
		uri.File("/w/defs.thrift"):   []byte("struct User { 1: i32 id }\n"),
		uri.File("/w/values.thrift"): []byte("const i32 User = 3\n"),
		uri.File("/w/app.thrift"): []byte("include \"defs.thrift\"\ninclude \"values.thrift\"\n" +
			"struct S { 1: User u = User }\n"),
	}, true)

	app, err := view.Parse(ctx, uri.File("/w/app.thrift"))
	require.NoError(t, err)

	type answer = mdlAnswer

	resolveBoth := func(ix *sema.Index, typeFirst bool) (answer, answer) {
		typeAnswer := func() answer {
			def, err := ix.ResolveType(ctx, app, mdlFieldType("User"))
			require.NoError(t, err)

			if def == nil {
				return answer{}
			}

			return answer{ok: true, file: def.File, name: def.Name.Text, kind: def.Kind}
		}
		valueAnswer := func() answer {
			def, err := ix.ResolveValue(ctx, app, &syntax.ConstValue{Kind: syntax.ValueIdent, Text: "User"})
			require.NoError(t, err)

			if def == nil {
				return answer{}
			}

			return answer{ok: true, file: def.File, name: def.Name.Text, kind: def.Kind}
		}

		if typeFirst {
			first := typeAnswer()

			return first, valueAnswer()
		}

		first := valueAnswer()

		return typeAnswer(), first
	}

	wantType := answer{ok: true, file: uri.File("/w/defs.thrift"), name: "User", kind: sema.DefinitionStruct}
	wantValue := answer{ok: true, file: uri.File("/w/values.thrift"), name: "User", kind: sema.DefinitionConst}

	for _, typeFirst := range []bool{true, false} {
		asType, asValue := resolveBoth(sema.NewIndex(view), typeFirst)
		assert.Equal(t, wantType, asType, "type resolution (type resolved first: %v)", typeFirst)
		assert.Equal(t, wantValue, asValue, "value resolution (type resolved first: %v)", typeFirst)
	}
}

// refsToCoverage counts the hits the ReferencesTo differential found, and the
// enum-qualified segment hits among them: both must occur for the property to
// mean anything.
type refsToCoverage struct {
	hits        int
	segmentHits int
}

// referencesToDifferential runs the ReferencesTo differential over the stream
// seed derives from. The counts say how many hits and enum-segment hits the
// run covered.
func referencesToDifferential(tb *testing.T, seed []byte) refsToCoverage {
	t := proptest.New(tb, seed)

	var cov refsToCoverage

	ctx := t.Context()
	ws := mdlDrawWorkspace(t)
	view := mdlNewView(ws.files, true)

	for _, target := range ws.model.defTargets() {
		def, err := mdlResolveDef(ctx, view, target)
		require.NoError(t, err)
		require.NotNil(t, def, "%s %q must resolve from %s", target.kind, target.name, target.file.Path())
		require.Equal(t, target.kind.semaKind(), def.Kind)

		var kinds []store.RefKind

		switch proptest.IntRange(0, 2).Draw(t) {
		case 0:
		case 1:
			kinds = sema.RefKindsFor(target.kind.semaKind())
		case 2:
			all := []store.RefKind{
				store.RefFieldType, store.RefSignatureType, store.RefConstValue,
				store.RefServiceExtends, store.RefAnnotationType,
			}
			kinds = []store.RefKind{all[proptest.IntRange(0, len(all)-1).Draw(t)]}
		}

		got, err := sema.NewIndex(view).ReferencesTo(ctx, def, kinds...)
		require.NoError(t, err)

		want := ws.model.referencesTo(target, kinds)
		require.Equal(t, want, mdlProductionHits(got),
			"ReferencesTo(%s %q) kinds %v", target.kind, target.name, kinds)

		for _, h := range want {
			cov.hits++

			// An enum-qualified hit covers the enum segment of a longer
			// value reference, not the whole reference.
			if target.kind == mdlEnum && h.kind == store.RefConstValue && h.text == target.name {
				cov.segmentHits++
			}
		}

		// Every hit must cover exactly the text it claims to cover, and
		// every hit re-resolved through a second index must land on the
		// same definition.
		for _, h := range mdlProductionHits(got) {
			content := ws.files[h.file]
			require.GreaterOrEqual(t, h.end, h.start)
			require.LessOrEqual(t, h.end, len(content))
			require.Equal(t, h.text, string(content[h.start:h.end]),
				"hit span must cover the hit text")

			if target.kind == mdlEnum && h.kind == store.RefConstValue && h.text == target.name {
				// An enum-qualified hit covers only the enum segment of a
				// longer value reference; the model already checked that
				// its qualifier resolves to the enum.
				continue
			}

			again := mdlResolveHitAgain(ctx, view, h)
			require.NotNil(t, again, "hit %q in %s matches no reference", h.text, h.file.Path())
			require.Equal(t, def.File, again.File, "hit %q re-resolves into another file", h.text)
			require.Equal(t, def.Name.Text, again.Name.Text, "hit %q re-resolves to another name", h.text)
		}
	}

	return cov
}

// FuzzIndexReferencesToDifferential compares ReferencesTo for every definition
// in the workspace (structs, unions, exceptions, enums, typedefs, consts,
// enum members, services) under all-kinds and per-kind filters.
func FuzzIndexReferencesToDifferential(f *testing.F) {
	proptest.Fuzz(f, resolveSeeds, func(tb *testing.T, seed []byte) {
		referencesToDifferential(tb, seed)
	})
}

// TestIndexReferencesToDifferentialCoversEnumSegments pins the generator
// coverage the ReferencesTo differential leans on.
func TestIndexReferencesToDifferentialCoversEnumSegments(t *testing.T) {
	var total refsToCoverage

	for _, seed := range resolveSeeds {
		c := referencesToDifferential(t, seed)
		total.hits += c.hits
		total.segmentHits += c.segmentHits
	}

	assert.Greater(t, total.hits, 0, "no reference to any definition was ever found")
	assert.Greater(t, total.segmentHits, 0, "no enum-qualified hit was ever produced")
}

// FuzzIndexReferencesNameMatchedDifferential compares the resolution-blind
// reference search: bare-name matching over the file and its transitive
// dependents, filtered by grammar slot.
func FuzzIndexReferencesNameMatchedDifferential(f *testing.F) {
	proptest.Fuzz(f, resolveSeeds, func(tb *testing.T, seed []byte) {
		t := proptest.New(tb, seed)

		ctx := t.Context()
		ws := mdlDrawWorkspace(t)
		view := mdlNewView(ws.files, true)

		for _, u := range ws.model.uris {
			d := ws.model.docs[u]

			names := append([]string(nil), mdlNames...)
			for _, def := range d.defs {
				names = append(names, def.name)
			}

			for _, name := range names {
				var kinds []store.RefKind

				switch proptest.IntRange(0, 3).Draw(t) {
				case 0:
				case 1:
					kinds = []store.RefKind{store.RefFieldType}
				case 2:
					kinds = []store.RefKind{store.RefConstValue}
				case 3:
					kinds = []store.RefKind{store.RefServiceExtends, store.RefSignatureType}
				}

				got, err := sema.NewIndex(view).References(ctx, u, name, kinds...)
				require.NoError(t, err)

				require.Equal(t, ws.model.references(u, name, kinds), mdlProductionHits(got),
					"References(%q) in %s kinds %v", name, u.Path(), kinds)
			}
		}
	})
}

// underlyingCoverage counts how the generated typedef chains ended: at a base
// type, at a container, at an unresolvable name, or on a named non-typedef
// type. Every ending must occur for the differential to mean anything.
type underlyingCoverage struct {
	nilChains       int
	baseChains      int
	containerChains int
	identChains     int
}

// underlyingTypeDifferential runs the UnderlyingType differential over the
// stream seed derives from. The counts say how the generated typedef chains
// ended.
func underlyingTypeDifferential(tb *testing.T, seed []byte) underlyingCoverage {
	t := proptest.New(tb, seed)

	var cov underlyingCoverage

	ctx := t.Context()
	ws := mdlDrawWorkspace(t)
	view := mdlNewView(ws.files, true)

	for _, u := range ws.model.uris {
		d := ws.model.docs[u]

		pf, err := view.Parse(ctx, u)
		require.NoError(t, err)

		seen := map[string]bool{}

		var texts []string

		for _, r := range d.refs {
			if r.kind == store.RefConstValue || r.kind == store.RefServiceExtends || seen[r.text] {
				continue
			}

			seen[r.text] = true
			texts = append(texts, r.text)
		}

		for _, def := range d.defs {
			if def.kind == mdlTypedef && !seen[def.name] {
				seen[def.name] = true
				texts = append(texts, def.name)
			}
		}

		ix := sema.NewIndex(view)

		for _, text := range texts {
			got, gotFile := ix.UnderlyingType(ctx, pf, mdlFieldType(text))
			want, wantFile := ws.model.underlyingType(d, &mdlType{ident: text})

			require.Equal(t, mdlModelDesc(want), mdlDescOf(got), "UnderlyingType(%q) from %s", text, u.Path())
			require.Equal(t, wantFile.uri, gotFile.URI(), "scope file of UnderlyingType(%q)", text)

			switch {
			case want == nil:
				cov.nilChains++
			case want.base != "":
				cov.baseChains++
			case want.container != "":
				cov.containerChains++
			default:
				cov.identChains++
			}
		}

		// A container is not an identifier: it is its own underlying type.
		container := &syntax.FieldType{Kind: syntax.TypeList, ValueType: mdlFieldType("T")}
		got, gotFile := ix.UnderlyingType(ctx, pf, container)
		require.Equal(t, "list", mdlDescOf(got))
		require.Equal(t, u, gotFile.URI())

		// A nil type has no classifiable kind.
		got, gotFile = ix.UnderlyingType(ctx, pf, nil)
		require.Equal(t, "<nil>", mdlDescOf(got))
		require.Equal(t, u, gotFile.URI())
	}

	return cov
}

// FuzzIndexUnderlyingTypeDifferential compares typedef-chain following with
// the model: chains that cross files, stop at a container or a struct, hit an
// unresolvable name, or cycle until the depth cap.
func FuzzIndexUnderlyingTypeDifferential(f *testing.F) {
	proptest.Fuzz(f, resolveSeeds, func(tb *testing.T, seed []byte) {
		underlyingTypeDifferential(tb, seed)
	})
}

// TestIndexUnderlyingTypeDifferentialCoversChainEnds pins the generator
// coverage the UnderlyingType differential leans on.
func TestIndexUnderlyingTypeDifferentialCoversChainEnds(t *testing.T) {
	var total underlyingCoverage

	for _, seed := range resolveSeeds {
		c := underlyingTypeDifferential(t, seed)
		total.nilChains += c.nilChains
		total.baseChains += c.baseChains
		total.containerChains += c.containerChains
		total.identChains += c.identChains
	}

	assert.Greater(t, total.baseChains, 0, "no generated typedef chain ever reached a base type")
	assert.Greater(t, total.containerChains, 0, "no generated typedef chain ever reached a container")
	assert.Greater(t, total.nilChains, 0, "no generated chain was ever unresolvable")
	assert.Greater(t, total.identChains, 0, "no generated chain ever ended on a named non-typedef type")
}

// FuzzIndexFindInWorkspaceDifferential compares the workspace-wide lookup with
// the model, both through the known-files table and through the directory-walk
// fallback an unindexed workspace takes.
func FuzzIndexFindInWorkspaceDifferential(f *testing.F) {
	proptest.Fuzz(f, resolveSeeds, func(tb *testing.T, seed []byte) {
		t := proptest.New(tb, seed)

		ctx := t.Context()
		ws := mdlDrawWorkspace(t)
		view := mdlNewView(ws.files, true)

		for _, u := range ws.model.uris {
			d := ws.model.docs[u]

			names := append([]string(nil), mdlNames...)
			for _, def := range d.defs {
				names = append(names, def.name)
			}

			for _, name := range names {
				queries := []string{name}
				for _, f := range ws.model.uris {
					queries = append(queries, mdlIncludeNameOf(f)+"."+name)
				}

				for _, query := range queries {
					got, err := sema.NewIndex(view).FindInWorkspace(ctx, query)
					require.NoError(t, err)

					require.Equal(t, mdlWorkspaceAnswer(got),
						mdlWorkspaceWant(ws.model, query), "FindInWorkspace(%q)", query)
				}
			}
		}

		// The same lookups in a view that has indexed nothing: the walk must
		// find what the known-files table found.
		for _, query := range append([]string(nil), mdlNames...) {
			unindexed := mdlNewView(ws.files, false)

			got, err := sema.NewIndex(unindexed).FindInWorkspace(ctx, query)
			require.NoError(t, err)
			require.Equal(t, mdlWorkspaceWant(ws.model, query),
				mdlWorkspaceAnswer(got),
				"walk fallback for %q", query)
		}
	})
}

// mdlWorkspaceWant is the model's expected workspace lookup answer.
func mdlWorkspaceWant(m *resolveModel, query string) mdlAnswer {
	target, ok := m.findInWorkspace(query, m.uris)
	if !ok {
		return mdlAnswer{}
	}

	return mdlAnswer{ok: true, file: target.file, name: target.name, kind: target.kind.semaKind()}
}

// mdlWorkspaceAnswer renders a production FindInWorkspace result for
// comparison. The known-files input is the view's own table, so the assertion
// is about which definition wins, not about how files are enumerated.
func mdlWorkspaceAnswer(got *sema.Resolved) mdlAnswer {
	if got == nil {
		return mdlAnswer{}
	}

	return mdlAnswer{ok: true, file: got.File, name: got.Name.Text, kind: got.Kind}
}

// TestQualifiedIncludeReferenceResolves checks include-name qualification:
// after include "shared.thrift", a type is spelled shared.User.
func TestQualifiedIncludeReferenceResolves(t *testing.T) {
	ctx := t.Context()

	view := mdlNewView(map[uri.URI][]byte{
		uri.File("/w/shared.thrift"): []byte("struct User { 1: i32 id }\nenum Color { RED = 1 }\nconst i32 MAX = 9\n"),
		uri.File("/w/app.thrift"): []byte("include \"shared.thrift\"\n" +
			"struct S { 1: shared.User u, 2: i32 x = shared.Color.RED, 3: i32 m = shared.MAX }\n"),
	}, true)

	app, err := view.Parse(ctx, uri.File("/w/app.thrift"))
	require.NoError(t, err)

	ix := sema.NewIndex(view)

	def, err := ix.ResolveType(ctx, app, mdlFieldType("shared.User"))
	require.NoError(t, err)
	require.NotNil(t, def, "shared.User must resolve")
	assert.Equal(t, uri.File("/w/shared.thrift"), def.File)
	assert.Equal(t, "User", def.Name.Text)
	assert.Equal(t, sema.DefinitionStruct, def.Kind)

	value, err := ix.ResolveValue(ctx, app, &syntax.ConstValue{Kind: syntax.ValueIdent, Text: "shared.Color.RED"})
	require.NoError(t, err)
	require.NotNil(t, value, "shared.Color.RED must resolve")
	assert.Equal(t, sema.DefinitionEnumValue, value.Kind)

	constDef, err := ix.ResolveValue(ctx, app, &syntax.ConstValue{Kind: syntax.ValueIdent, Text: "shared.MAX"})
	require.NoError(t, err)
	require.NotNil(t, constDef, "shared.MAX must resolve")
	assert.Equal(t, sema.DefinitionConst, constDef.Kind)

	enumDef, err := ix.ResolveType(ctx, app, mdlFieldType("shared.Color"))
	require.NoError(t, err)
	require.NotNil(t, enumDef)

	// The qualified references are reported as references to the definition.
	hits, err := ix.ReferencesTo(ctx, def)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "shared.User", hits[0].Text)

	enumHits, err := ix.ReferencesTo(ctx, enumDef, store.RefConstValue)
	require.NoError(t, err)
	require.Len(t, enumHits, 1)
	assert.Equal(t, "Color", enumHits[0].Text, "an enum-qualified hit covers only the enum segment")
}

// TestQualifiedReferenceBindsLongestIncludeName checks that the longest
// matching include name wins: user.extra.T refers to the file included as
// "user.extra".
func TestQualifiedReferenceBindsLongestIncludeName(t *testing.T) {
	ctx := t.Context()

	view := mdlNewView(map[uri.URI][]byte{
		uri.File("/w/user.thrift"):       []byte("struct T { 1: i32 id }\n"),
		uri.File("/w/user.extra.thrift"): []byte("struct T { 1: string name }\n"),
		uri.File("/w/app.thrift"): []byte("include \"user.thrift\"\ninclude \"user.extra.thrift\"\n" +
			"struct S { 1: user.extra.T a, 2: user.T b }\n"),
	}, true)

	app, err := view.Parse(ctx, uri.File("/w/app.thrift"))
	require.NoError(t, err)

	ix := sema.NewIndex(view)

	extra, err := ix.ResolveType(ctx, app, mdlFieldType("user.extra.T"))
	require.NoError(t, err)
	require.NotNil(t, extra, "user.extra.T must resolve through the longer include name")
	assert.Equal(t, uri.File("/w/user.extra.thrift"), extra.File)

	plain, err := ix.ResolveType(ctx, app, mdlFieldType("user.T"))
	require.NoError(t, err)
	require.NotNil(t, plain, "user.T must resolve through the shorter include name")
	assert.Equal(t, uri.File("/w/user.thrift"), plain.File)
}

// TestDottedFilenameReferenceDoesNotResolve checks that the filename spelling
// "shared.thrift.User" resolves nowhere and is left untouched by rename.
func TestDottedFilenameReferenceDoesNotResolve(t *testing.T) {
	ctx := t.Context()

	view := mdlNewView(map[uri.URI][]byte{
		uri.File("/w/shared.thrift"): []byte("struct User { 1: i32 id }\n"),
		uri.File("/w/app.thrift"): []byte("include \"shared.thrift\"\n" +
			"struct S { 1: shared.thrift.User u, 2: shared.User v }\n"),
	}, true)

	app, err := view.Parse(ctx, uri.File("/w/app.thrift"))
	require.NoError(t, err)
	require.Empty(t, app.Errors())

	// The reference is recorded as written, raw and uninterpreted.
	var texts []string

	for _, r := range app.Index().References() {
		texts = append(texts, r.Name)
	}

	require.Equal(t, []string{"shared.thrift.User", "shared.User"}, texts)

	ix := sema.NewIndex(view)

	define, err := ix.ResolveType(ctx, app, mdlFieldType("shared.User"))
	require.NoError(t, err)
	require.NotNil(t, define)

	def, err := ix.ResolveType(ctx, app, mdlFieldType("shared.thrift.User"))
	require.NoError(t, err)
	assert.Nil(t, def, "the dotted-filename spelling is not a valid reference and must not resolve")

	// Consequently a rename of the definition rewrites only the valid
	// spelling: exactly one hit, the two-segment one.
	hits, err := ix.ReferencesTo(ctx, define, store.RefFieldType)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "shared.User", hits[0].Text)
	assert.Equal(t, "shared.User", string([]byte("include \"shared.thrift\"\nstruct S { 1: shared.thrift.User u, 2: shared.User v }\n")[hits[0].Span.Start.Offset:hits[0].Span.End.Offset]))
}

func TestUnderlyingType(t *testing.T) {
	a := uri.File("/w/a.thrift")
	b := uri.File("/w/b.thrift")

	for _, tt := range []struct {
		name      string
		files     map[uri.URI][]byte
		query     string
		want      string
		wantScope uri.URI
	}{
		{
			name:  "same file cycle",
			files: map[uri.URI][]byte{a: []byte("typedef B A\ntypedef A B\n")},
			query: "A",
			want:  "<nil>",
		},
		{
			name: "cross file cycle",
			files: map[uri.URI][]byte{
				a: []byte(`include "b.thrift"
typedef b.B A
typedef B A2
`),
				b: []byte(`include "a.thrift"
typedef a.A2 B
typedef B A3
`),
			},
			query: "A",
			want:  "<nil>",
		},
		{
			name:      "chain to base type",
			files:     map[uri.URI][]byte{a: []byte("typedef i32 Age\ntypedef Age Years\n")},
			query:     "Years",
			want:      "base(i32)",
			wantScope: a,
		},
		{
			name:      "chain to container",
			files:     map[uri.URI][]byte{a: []byte("typedef i32 Age\ntypedef list<Age> Ages\n")},
			query:     "Ages",
			want:      "list",
			wantScope: a,
		},
		{
			name: "chain through include",
			files: map[uri.URI][]byte{
				a: []byte(`include "b.thrift"
typedef b.Other Mine
`),
				b: []byte("typedef string Other\n"),
			},
			query:     "Mine",
			want:      "base(string)",
			wantScope: b,
		},
		{
			name:  "unresolved identifier",
			files: map[uri.URI][]byte{a: []byte("typedef Ghost A\n")},
			query: "A",
			want:  "<nil>",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			view := mdlNewView(tt.files, true)

			pf, err := view.Parse(ctx, a)
			require.NoError(t, err)

			got, scope := sema.NewIndex(view).UnderlyingType(ctx, pf, mdlFieldType(tt.query))
			assert.Equal(t, tt.want, mdlDescOf(got))

			if tt.wantScope != "" {
				require.NotNil(t, scope)
				assert.Equal(t, tt.wantScope, scope.URI())
			}
		})
	}
}
