package source

import (
	"path/filepath"
	"sort"
	"strings"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/syntax"
)

// includeNameOf returns the include name of a file URI: the base name
// without extension. file:///base.thrift -> "base".
func includeNameOf(file uri.URI) string {
	fileName := file.Path()

	index := strings.LastIndexByte(fileName, filepath.Separator)
	if index != -1 {
		fileName = string(fileName[index+1:])
	}

	index = strings.LastIndexByte(fileName, '.')
	if index == -1 {
		return fileName
	}

	return string(fileName[0:index])
}

// parseIdent parses an identifier. identifier format:
//  1. identifier
//  2. include.identifier
//
// it returns include, ident
func parseIdent(cur uri.URI, includes []*syntax.Include, identifier string) (include, ident string) {
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

// includeNames returns include names from include ast nodes
func includeNames(cur uri.URI, includes []*syntax.Include) (includeNames []string) {
	for _, inc := range includes {
		if path := inc.PathText(); path != "" {
			u := uri.File(filepath.Join(filepath.Dir(cur.Path()), path))
			includeNames = append(includeNames, includeNameOf(u))
		}
	}

	return includeNames
}
