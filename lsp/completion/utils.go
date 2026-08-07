package completion

import (
	"os"
	"path/filepath"
	"strings"

	"go.lsp.dev/protocol"
)

// ListDirAndFiles lists the entries matching the typed include path prefix,
// one level deep, under the current file's directory and every configured
// include path root. Directories are returned with a trailing slash; only
// .thrift files are returned. Results are deduplicated across roots.
func ListDirAndFiles(dir string, includePaths []string, prefix string) []Candidate {
	prefix = strings.Trim(prefix, "'\"")

	roots := make([]string, 0, 1+len(includePaths))
	if dir != "" {
		roots = append(roots, dir)
	}

	for _, p := range includePaths {
		if p != "" {
			roots = append(roots, p)
		}
	}

	// Split the typed prefix into the directory part (resolved per root)
	// and the file prefix.
	dirPart, filePrefix := "", prefix
	if i := strings.LastIndex(prefix, "/"); i >= 0 {
		dirPart, filePrefix = prefix[:i+1], prefix[i+1:]
	}

	seen := make(map[string]struct{})

	var res []Candidate

	for _, root := range roots {
		entries, err := os.ReadDir(filepath.Join(root, dirPart))
		if err != nil {
			continue
		}

		for _, e := range entries {
			name := e.Name()
			if !strings.HasPrefix(name, filePrefix) {
				continue
			}

			var text string

			switch {
			case e.IsDir():
				text = filepath.Join(dirPart, name) + "/"
			case strings.HasSuffix(name, ".thrift"):
				text = filepath.Join(dirPart, name)
			default:
				continue
			}

			if _, ok := seen[text]; ok {
				continue
			}

			seen[text] = struct{}{}

			res = append(res, Candidate{
				showText:   text,
				insertText: text,
				format:     protocol.InsertTextFormatPlainText,
			})
		}
	}

	return res
}
