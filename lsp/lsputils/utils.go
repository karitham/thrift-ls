package lsputils

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/syntax"
)

// GetIncludeName return include name by file uri
// for example: file uri is file:///base.thrift, then `base` is include name
func GetIncludeName(file uri.URI) string {
	fileName := file.Path()

	index := strings.LastIndexByte(fileName, filepath.Separator)
	if index == -1 {
		return fileName
	}

	fileName = string(fileName[index+1:])

	index = strings.LastIndexByte(fileName, '.')
	if index == -1 {
		return fileName
	}

	return string(fileName[0:index])
}

// IncludePathText returns the include path of an include node without its
// quotes. The syntax token keeps the raw literal text, including quotes.
func IncludePathText(inc *syntax.Include) string {
	if inc == nil || inc.Path == nil {
		return ""
	}

	return strings.Trim(inc.Path.Text, "\"'")
}

// includeName: base.User. `base` is the includeName. returns ../../base.thrift
// if doesn't match, return empty string
func GetIncludePath(ast *syntax.Document, includeName string) string {
	for _, include := range ast.Includes() {
		path := IncludePathText(include)
		if path == "" {
			continue
		}

		items := strings.Split(path, "/")

		path = items[len(items)-1]
		if !strings.HasSuffix(path, ".thrift") {
			continue
		}

		name := strings.TrimSuffix(path, ".thrift")
		if name == includeName {
			return IncludePathText(include)
		}
	}

	return ""
}

// cur is current file uri. for example file:///tmp/user.thrift
// includePath is include name used in code. for example: base.thrift
func IncludeURI(cur uri.URI, includePath string) uri.URI {
	filePath := cur.Path()
	items := strings.Split(filePath, string(filepath.Separator))
	basePath := strings.TrimSuffix(filePath, items[len(items)-1])

	path := filepath.Join(basePath, includePath)

	return uri.File(path)
}

// IncludeURIWithPaths resolves include path, first trying relative to current file,
// then trying each include path
func IncludeURIWithPaths(cur uri.URI, includePath string, includePaths []string) uri.URI {
	filePath := cur.Path()
	items := strings.Split(filePath, string(filepath.Separator))
	basePath := strings.TrimSuffix(filePath, items[len(items)-1])
	path := filepath.Join(basePath, includePath)
	f := uri.File(path)

	// Check if file exists - if so, use it
	if _, err := os.Stat(path); err == nil {
		return f
	}

	// Try each include path
	for _, ip := range includePaths {
		ipath := filepath.Join(ip, includePath)
		if _, err := os.Stat(ipath); err == nil {
			return uri.File(ipath)
		}
	}

	// Return the relative path as fallback
	return f
}

// ParseIdent parse an identifier. identifier format:
//  1. identifier
//  2. include.identifier
//
// it returns include, ident
func ParseIdent(cur uri.URI, includes []*syntax.Include, identifier string) (include, ident string) {
	includeNames := IncludeNames(cur, includes)
	// parse include from includeNames

	sort.SliceStable(includeNames, func(i, j int) bool {
		// sort by string length, make sure longest include match early
		// examples:
		// user.extra
		// user
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
func IncludeNames(cur uri.URI, includes []*syntax.Include) (includeNames []string) {
	for _, inc := range includes {
		path := IncludePathText(inc)
		if path != "" {
			u := IncludeURI(cur, path)
			includeName := GetIncludeName(u)
			includeNames = append(includeNames, includeName)
		}
	}

	return includeNames
}
