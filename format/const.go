package format

import (
	"github.com/joyme123/thrift-ls/parser"
)

const constOneLineTpl = `{{.Comments}}{{.Const}} {{.Type}} {{.Name}} {{.Equal}} {{.Value}}{{.Annotations}}{{.ListSeparator}}{{.EndLineComments}}
`

type ConstFormatter struct {
	Comments        string
	Const           string
	Type            string
	Name            string
	Annotations     string
	Equal           string
	Value           string
	ListSeparator   string
	EndLineComments string
}

func MustFormatConst(cst *parser.Const, opts Options) string {
	comments, annos := formatCommentsAndAnnos(opts, cst.Comments, cst.Annotations, "")
	if len(cst.Comments) > 0 && lineDistance(cst.Comments[len(cst.Comments)-1], cst.ConstKeyword) > 1 {
		comments = comments + "\n"
	}

	sep := ""
	if cst.ListSeparatorKeyword != nil {
		sep = MustFormatKeyword(opts, cst.ListSeparatorKeyword.Keyword)
	}

	f := &ConstFormatter{
		Comments:        comments,
		Const:           MustFormatKeyword(opts, cst.ConstKeyword.Keyword),
		Type:            MustFormatFieldType(cst.ConstType, opts),
		Name:            MustFormatIdentifier(opts, cst.Name, ""),
		Annotations:     annos,
		Equal:           MustFormatKeyword(opts, cst.EqualKeyword.Keyword),
		Value:           MustFormatConstValue(cst.Value, opts, "", false),
		ListSeparator:   sep,
		EndLineComments: MustFormatEndLineComments(opts, cst.EndLineComments, "", ""),
	}

	return MustFormat(constOneLineTpl, f)
}
