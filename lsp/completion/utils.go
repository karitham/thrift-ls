package completion

import (
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/constants"
)

func ListDirAndFiles(dir, prefix string) (res []Candidate, err error) {
	// handle prefix list ../../us
	prefixClean := prefix
	if len(prefix) > 0 {
		prefixClean = filepath.Clean(prefix)
	}

	if prefix == "." {
		prefix = prefix + "/"
	}

	up := strings.Count(prefixClean, "../")

	pathItems := strings.Split(dir, "/")
	if len(pathItems) < up {
		return res, err
	}

	pathItems = pathItems[0 : len(pathItems)-up]

	dir, filePrefix := filepath.Split(strings.TrimPrefix(prefixClean, "../"))
	filePrefix = strings.TrimPrefix(filePrefix, "./")
	baseDir := strings.Join(pathItems, "/") + "/" + dir
	prefix = strings.TrimSuffix(prefix, filePrefix)

	slog.Debug("include completion walk dir", "dir", baseDir, "prefix", prefix, "filePrefix", filePrefix)
	_ = filepath.WalkDir(baseDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || baseDir == path {
			return nil
		}
		slog.Debug("include completion name", "name", d.Name(), "prefix", filePrefix)
		if strings.HasPrefix(d.Name(), filePrefix) {
			if d.IsDir() {
				res = append(res, Candidate{
					showText:   prefix + d.Name() + "/",
					insertText: prefix + d.Name() + "/",
					format:     protocol.InsertTextFormatPlainText,
				})
			} else if strings.HasSuffix(d.Name(), constants.ThriftExtension) {
				res = append(res, Candidate{
					showText:   prefix + d.Name(),
					insertText: prefix + d.Name(),
					format:     protocol.InsertTextFormatPlainText,
				})
			}
		}

		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})

	return res, err
}
