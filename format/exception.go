package format

import (
	"github.com/joyme123/thrift-ls/parser"
)

const (
	exceptionOneLineTpl = `{{.Comments}}{{.Exception}} {{.Identifier}} {{.LCUR}}{{.RCUR}}{{.Annotations}}{{.EndLineComments}}`

	exceptionMultiLineTpl = `{{.Comments}}{{.Exception}} {{.Identifier}} {{.LCUR}}
{{.Fields}}{{.RCUR}}{{.Annotations}}{{.EndLineComments}}
`
)

type ExceptionFormatter struct {
	Comments        string
	Exception       string
	Identifier      string
	LCUR            string
	Fields          string
	RCUR            string
	Annotations     string
	EndLineComments string
}

func MustFormatException(excep *parser.Exception, opts Options) string {
	comments, annos := formatCommentsAndAnnos(opts, excep.Comments, excep.Annotations, "")
	if len(excep.Comments) > 0 && lineDistance(excep.Comments[len(excep.Comments)-1], excep.ExceptionKeyword) > 1 {
		comments = comments + "\n"
	}
	f := ExceptionFormatter{
		Comments:        comments,
		Exception:       MustFormatKeyword(opts, excep.ExceptionKeyword.Keyword),
		Identifier:      MustFormatIdentifier(opts, excep.Name, ""),
		LCUR:            MustFormatKeyword(opts, excep.LCurKeyword.Keyword),
		Fields:          MustFormatFields(excep.Fields, opts, opts.GetIndent()),
		RCUR:            MustFormatKeyword(opts, excep.RCurKeyword.Keyword),
		Annotations:     annos,
		EndLineComments: MustFormatEndLineComments(opts, excep.EndLineComments, "", ""),
	}

	if len(excep.Fields) > 0 {
		return MustFormat(exceptionMultiLineTpl, f)
	}

	return MustFormat(exceptionOneLineTpl, f)
}
