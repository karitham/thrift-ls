package sema

import (
	"path"
	"sort"
	"strings"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/syntax"
)

// IncludeNameOf returns the include name of a file URI: the base name
// without extension. file:///base.thrift -> "base".
func IncludeNameOf(file uri.URI) string {
	// URI paths are always slash-separated, even for Windows drive
	// letters, so path (not filepath) is the matching stdlib.
	fileName := path.Base(file.Path())

	index := strings.LastIndexByte(fileName, '.')
	if index == -1 {
		return fileName
	}

	return string(fileName[0:index])
}

// ParseIdent parses an identifier. identifier format:
//  1. identifier
//  2. include.identifier
//
// it returns include, ident
func ParseIdent(cur uri.URI, includes []*syntax.Include, identifier string) (include, ident string) {
	includeNames := includeNames(cur, includes)

	// sort by string length, make sure longest include match early
	// examples:
	// user.extra
	// user
	sort.SliceStable(includeNames, func(i, j int) bool {
		return len(includeNames[i]) > len(includeNames[j])
	})

	for _, incName := range includeNames {
		prefix := incName + "."
		if after, ok := strings.CutPrefix(identifier, prefix); ok {
			return incName, after
		}
	}

	return "", identifier
}

func splitQualifiedName(name string) (include, identifier string) {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[:i], name[i+1:]
	}

	return "", name
}

// includeNames returns include names from include ast nodes
func includeNames(cur uri.URI, includes []*syntax.Include) (includeNames []string) {
	for _, inc := range includes {
		if p := inc.PathText(); p != "" {
			u := uri.File(path.Join(path.Dir(cur.Path()), p))
			includeNames = append(includeNames, IncludeNameOf(u))
		}
	}

	return includeNames
}
