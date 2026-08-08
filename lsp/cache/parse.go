package cache

import (
	"fmt"
	"log/slog"
	"sync"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/mapper"
	"github.com/karitham/thrift-ls/syntax"
)

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
	tokensOnce sync.Once
	tokens     map[string]struct{}

	// index is the file's semantic index, computed lazily once per parse.
	// A single walk of the AST collects definitions, enum values, name
	// references, and annotation names.
	indexOnce sync.Once
	index     *FileIndex
}

// Index returns the file's semantic index: definitions, enum values, name
// references, and annotation names from a single AST walk.
func (p *ParsedFile) Index() *FileIndex {
	p.indexOnce.Do(func() {
		p.index = buildIndex(p.ast)
	})

	return p.index
}

// URI returns the URI of the parsed file.
func (p *ParsedFile) URI() uri.URI {
	return p.fh.URI()
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
	p.tokensOnce.Do(func() {
		tokens := make(map[string]struct{})
		if p.ast != nil {
			collectTokens(p.ast, tokens)
		}

		p.tokens = tokens
	})

	return p.tokens
}

// Definitions returns the file's top-level definitions indexed by name:
// structs, unions, exceptions, enums, services, consts, and typedefs. The
// node's concrete type identifies the definition kind.
func (p *ParsedFile) Definitions() map[string]syntax.Node {
	return p.Index().Defs()
}

// EnumValues returns the file's enum value names indexed by name.
func (p *ParsedFile) EnumValues() map[string]*syntax.Identifier {
	return p.Index().EnumValues()
}

func (p *ParsedFile) AggregatedError() error {
	if len(p.errs) == 0 {
		return nil
	}

	return fmt.Errorf("aggregated error: %v", p.errs)
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

	pf.mapper = mapper.NewMapper(content)

	return pf, nil
}
