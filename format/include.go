package format

import (
	"github.com/joyme123/thrift-ls/parser"
)

const includeTpl = "{{.Comments}}{{.Include}} {{.Path}}{{.EndLineComments}}\n"

type IncludeFormatter struct {
	Comments        string
	Include         string
	Path            string
	EndLineComments string
}

func MustFormatInclude(inc *parser.Include, opts Options) string {
	comments, _ := formatCommentsAndAnnos(opts, inc.Comments, nil, "")
	if len(inc.Comments) > 0 && lineDistance(inc.Comments[len(inc.Comments)-1], inc.IncludeKeyword) > 1 {
		comments = comments + "\n"
	}

	f := &IncludeFormatter{
		Comments:        comments,
		Include:         MustFormatKeyword(opts, inc.IncludeKeyword.Keyword),
		Path:            MustFormatLiteral(opts, inc.Path, ""),
		EndLineComments: MustFormatComments(opts, inc.EndLineComments, "", ""),
	}

	return MustFormat(includeTpl, f)
}

func MustFormatCPPInclude(inc *parser.CPPInclude, opts Options) string {
	comments, _ := formatCommentsAndAnnos(opts, inc.Comments, nil, "")
	if len(inc.Comments) > 0 && lineDistance(inc.Comments[len(inc.Comments)-1], inc.CPPIncludeKeyword) > 1 {
		comments = comments + "\n"
	}

	f := &IncludeFormatter{
		Comments:        comments,
		Include:         MustFormatKeyword(opts, inc.CPPIncludeKeyword.Keyword),
		Path:            MustFormatLiteral(opts, inc.Path, ""),
		EndLineComments: MustFormatComments(opts, inc.EndLineComments, "", ""),
	}

	return MustFormat(includeTpl, f)
}
