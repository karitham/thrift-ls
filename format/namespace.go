package format

import (
	"github.com/joyme123/thrift-ls/parser"
)

const namespaceOneLineTpl = "{{.Comments}}{{.Namespace}} {{.Language}} {{.Name}}{{.Annotations}}{{.EndLineComments}}\n"

type NamespaceFormatter struct {
	Comments        string
	Namespace       string
	Language        string
	Name            string
	Annotations     string
	EndLineComments string
}

func MustFormatNamespace(ns *parser.Namespace, opts Options) string {
	comments, annos := formatCommentsAndAnnos(opts, ns.Comments, ns.Annotations, "")
	if len(ns.Comments) > 0 && lineDistance(ns.Comments[len(ns.Comments)-1], ns.NamespaceKeyword) > 1 {
		comments = comments + "\n"
	}

	f := &NamespaceFormatter{
		Comments:        comments,
		Namespace:       MustFormatKeyword(opts, ns.NamespaceKeyword.Keyword),
		Language:        MustFormatIdentifier(opts, &ns.Language.Identifier, ""),
		Name:            MustFormatIdentifier(opts, ns.Name, ""),
		Annotations:     annos,
		EndLineComments: MustFormatEndLineComments(opts, ns.EndLineComments, "", ""),
	}

	return MustFormat(namespaceOneLineTpl, f)
}
