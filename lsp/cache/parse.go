package cache

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/mapper"
	"github.com/karitham/thrift-ls/syntax"
)

// ParseCaches maps URIs to parsed files. Snapshots share the underlying map
// (Clone is O(1)); the first write after a clone copies the map
// copy-on-write, so cloning per keystroke is cheap while old snapshots stay
// immutable.
type ParseCaches struct {
	mu     sync.RWMutex
	caches map[uri.URI]*ParsedFile
	shared bool
}

func NewParseCaches() *ParseCaches {
	return &ParseCaches{
		caches: make(map[uri.URI]*ParsedFile),
	}
}

func (c *ParseCaches) Set(filePath uri.URI, res *ParsedFile) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.copyOnWrite()

	c.caches[filePath] = res
}

func (c *ParseCaches) Get(filePath uri.URI) *ParsedFile {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.caches[filePath]
}

func (c *ParseCaches) Forget(filePath uri.URI) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.copyOnWrite()

	delete(c.caches, filePath)
}

// Clone returns a view sharing the same entries. The clone and the original
// both become copy-on-write.
func (c *ParseCaches) Clone() *ParseCaches {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.shared = true

	return &ParseCaches{caches: c.caches, shared: true}
}

// copyOnWrite detaches caches from a shared parent before the first write.
// Callers must hold mu.
func (c *ParseCaches) copyOnWrite() {
	if !c.shared {
		return
	}

	caches := make(map[uri.URI]*ParsedFile, len(c.caches)+1)
	for k, v := range c.caches {
		caches[k] = v
	}

	c.caches = caches
	c.shared = false
}

// TokensForFile returns tokens for the given file and its transitively
// included files. Each file's token set is computed once per parse and
// reused, so typing does not re-walk the include closure's ASTs.
func (c *ParseCaches) TokensForFile(file uri.URI, getIncludes func(uri.URI) []uri.URI) map[string]struct{} {
	tokens := make(map[string]struct{})
	visited := make(map[uri.URI]bool)

	var collect func(f uri.URI)

	collect = func(f uri.URI) {
		if visited[f] {
			return
		}

		visited[f] = true

		if pf := c.Get(f); pf != nil {
			for token := range pf.Tokens() {
				tokens[token] = struct{}{}
			}
		}

		for _, inc := range getIncludes(f) {
			collect(inc)
		}
	}

	collect(file)

	return tokens
}

// collectTokens collects every identifier name in the document: definition
// names, field names, and identifier references.
func collectTokens(ast *syntax.Document, tokens map[string]struct{}) {
	var walk func(n syntax.Node)

	walk = func(n syntax.Node) {
		switch v := n.(type) {
		case *syntax.Identifier:
			tokens[v.Text] = struct{}{}
		case *syntax.ConstValue:
			if v.Kind == syntax.ValueIdent {
				tokens[v.Text] = struct{}{}
			}

			for _, item := range v.List {
				walk(item)
			}

			for _, entry := range v.Map {
				walk(entry.Key)
				walk(entry.Value)
			}
		case *syntax.Struct:
			walk(v.Name)

			for _, f := range v.Fields {
				walk(f)
			}
		case *syntax.Service:
			walk(v.Name)

			for _, fn := range v.Functions {
				walk(fn)
			}
		case *syntax.Enum:
			walk(v.Name)

			for _, ev := range v.Values {
				walk(ev)
			}
		case *syntax.Field:
			walk(v.Type)
			walk(v.Name)

			if v.Value != nil {
				walk(v.Value)
			}
		case *syntax.Function:
			if v.Type != nil {
				walk(v.Type)
			}

			walk(v.Name)

			for _, a := range v.Args {
				walk(a)
			}

			if v.Throws != nil {
				for _, f := range v.Throws.Fields {
					walk(f)
				}
			}
		case *syntax.FieldType:
			if v.Ident != nil {
				walk(v.Ident)
			}

			if v.KeyType != nil {
				walk(v.KeyType)
			}

			if v.ValueType != nil {
				walk(v.ValueType)
			}
		case *syntax.Const:
			walk(v.Type)
			walk(v.Name)
			walk(v.Value)
		case *syntax.Typedef:
			walk(v.Type)
			walk(v.Name)
		case *syntax.EnumValue:
			walk(v.Name)
		}
	}
	for _, n := range ast.Nodes {
		walk(n)
	}
}

type ParsedFile struct {
	fh FileHandle
	// ast is the latest available ast. The current fh content may not be
	// parsed, so ast may be nil when fh content is invalid.
	ast *syntax.Document

	mapper *mapper.Mapper

	// errs hold all ast parsing errors
	errs []syntax.Error

	// tokens is the identifier set of ast, computed lazily once per parse.
	tokens map[string]struct{}
}

func (p *ParsedFile) Mapper() *mapper.Mapper {
	return p.mapper
}

func (p *ParsedFile) AST() *syntax.Document {
	return p.ast
}

func (p *ParsedFile) Errors() []syntax.Error {
	return p.errs
}

// Tokens returns the identifier tokens of the file, computed once and
// reused. A re-parse replaces the whole ParsedFile, so the cache never
// goes stale.
func (p *ParsedFile) Tokens() map[string]struct{} {
	if p.tokens != nil {
		return p.tokens
	}

	tokens := make(map[string]struct{})
	if p.ast != nil {
		collectTokens(p.ast, tokens)
	}

	p.tokens = tokens

	return tokens
}

func (p *ParsedFile) AggregatedError() error {
	if len(p.errs) == 0 {
		return nil
	}

	return fmt.Errorf("aggregated error: %v", p.errs)
}

// DumpAST is for debug.
func (p *ParsedFile) DumpAST() {
	if p.ast == nil {
		return
	}

	data, _ := json.MarshalIndent(p.ast, "", "  ")
	fmt.Println(string(data))
}

// Parse lexes and parses the file content into a ParsedFile.
func Parse(fh FileHandle) (*ParsedFile, error) {
	content, err := fh.Content()
	if err != nil {
		return nil, err
	}

	pf := &ParsedFile{
		fh: fh,
	}

	ast, errs := syntax.Parse(content)
	pf.errs = errs
	pf.ast = ast

	if len(errs) > 0 {
		slog.Debug("parse failed", "errs", errs)
	}

	pf.mapper = mapper.NewMapper(fh.URI(), content)

	return pf, nil
}
