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

type ParseCaches struct {
	mu     sync.RWMutex
	caches map[uri.URI]*ParsedFile
	tokens map[string]struct{}
}

func NewParseCaches() *ParseCaches {
	return &ParseCaches{
		caches: make(map[uri.URI]*ParsedFile),
	}
}

func (c *ParseCaches) Set(filePath uri.URI, res *ParsedFile) {
	c.mu.Lock()
	c.caches[filePath] = res
	c.tokens = nil
	c.mu.Unlock()
}

func (c *ParseCaches) Get(filePath uri.URI) *ParsedFile {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.caches[filePath]
}

func (c *ParseCaches) Forget(filePath uri.URI) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.caches, filePath)
	c.tokens = nil
}

func (c *ParseCaches) Clone() *ParseCaches {
	c.mu.RLock()
	defer c.mu.RUnlock()

	clone := make(map[uri.URI]*ParsedFile)
	for i := range c.caches {
		clone[i] = c.caches[i]
	}
	return &ParseCaches{caches: clone}
}

func (c *ParseCaches) Tokens() map[string]struct{} {
	if len(c.tokens) > 0 {
		return c.tokens
	}

	tokens := make(map[string]struct{})
	for _, parsed := range c.caches {
		if parsed.ast == nil {
			continue
		}
		collectTokens(parsed.ast, tokens)
	}
	c.tokens = tokens

	return tokens
}

// TokensForFile returns tokens for the given file and its transitively
// included files.
func (c *ParseCaches) TokensForFile(file uri.URI, getIncludes func(uri.URI) []uri.URI) map[string]struct{} {
	tokens := make(map[string]struct{})
	visited := make(map[uri.URI]bool)

	var collect func(f uri.URI)
	collect = func(f uri.URI) {
		if visited[f] {
			return
		}
		visited[f] = true

		pf := c.Get(f)
		if pf != nil && pf.ast != nil {
			collectTokens(pf.ast, tokens)
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
