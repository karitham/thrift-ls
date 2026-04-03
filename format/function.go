package format

import (
	"bytes"

	"github.com/joyme123/thrift-ls/parser"
)

func MustFormatFunctions(fns []*parser.Function, opts Options, indent string) string {
	buf := bytes.NewBuffer(nil)
	fmtCtx := &fmtContext{}
	for i := range fns {
		if needAddtionalLineForFuncs(fmtCtx.preNode, fns[i]) {
			buf.WriteString("\n")
		}
		buf.WriteString(MustFormatFunction(fns[i], opts, indent))
		if i < len(fns)-1 {
			buf.WriteString("\n")
		}
		fmtCtx.preNode = fns[i]
	}

	return buf.String()
}

const functionTpl = "{{.Oneway}}{{.FunctionType}} {{.Identifier}}{{.LPAR}}{{.Args}}{{.RPAR}}{{.Throws}}{{.Annotations}}{{.ListSeparator}}{{.EndLineComments}}"

type FunctionFormatter struct {
	Oneway          string
	FunctionType    string
	Identifier      string
	LPAR            string
	Args            string
	RPAR            string
	Throws          string
	Annotations     string
	ListSeparator   string
	EndLineComments string
}

func MustFormatFunction(fn *parser.Function, opts Options, indent string) string {
	comments, annos := formatCommentsAndAnnos(opts, fn.Comments, fn.Annotations, indent)
	var firstNode parser.Node
	if fn.Void != nil {
		firstNode = fn.Void
	} else {
		firstNode = fn.FunctionType
	}
	if len(fn.Comments) > 0 && lineDistance(fn.Comments[len(fn.Comments)-1], firstNode) > 1 {
		comments = comments + "\n"
	}

	oneway := ""
	if fn.Oneway != nil {
		oneway = "oneway "
	}
	args := ""
	if len(fn.Arguments) > 0 {
		args = MustFormatOneLineFields(fn.Arguments, opts)
	}

	ft := ""
	if fn.Void != nil {
		ft = MustFormatKeyword(opts, fn.Void.Keyword)
	} else {
		ft = MustFormatFieldType(fn.FunctionType, opts)
	}

	sep := ""

	if opts.getFieldLineComma() == FieldLineCommaAdd { // add comma always
		sep = ","
	} else if opts.getFieldLineComma() == FieldLineCommaDisable { // add list separator
		if fn.ListSeparatorKeyword != nil {
			sep = MustFormatKeyword(opts, fn.ListSeparatorKeyword.Keyword)
		}
	} // otherwise, sep will be removed

	throws := MustFormatThrows(fn.Throws, opts)
	if fn.Throws != nil {
		throws = " " + throws
	}

	f := &FunctionFormatter{
		Oneway:          oneway,
		FunctionType:    ft,
		Identifier:      MustFormatIdentifier(opts, fn.Name, ""),
		LPAR:            MustFormatKeyword(opts, fn.LParKeyword.Keyword),
		Args:            args,
		RPAR:            MustFormatKeyword(opts, fn.RParKeyword.Keyword),
		Throws:          throws,
		Annotations:     annos,
		ListSeparator:   sep,
		EndLineComments: MustFormatEndLineComments(opts, fn.EndLineComments, "", ""),
	}

	fnStr := MustFormat(functionTpl, f)
	fnStr = comments + indent + fnStr

	return fnStr
}

const throwTpl = "{{.Throw}} {{.LPAR}}{{.Fields}}{{.RPAR}}"

type ThrowFormatter struct {
	Throw  string
	LPAR   string
	Fields string
	RPAR   string
}

func MustFormatThrows(throws *parser.Throws, opts Options) string {
	if throws == nil {
		return ""
	}

	args := ""
	if len(throws.Fields) > 0 {
		indent := opts.GetIndent()
		args = "\n" + MustFormatFields(throws.Fields, opts, indent+indent) + indent
	}

	f := &ThrowFormatter{
		Throw:  MustFormatKeyword(opts, throws.ThrowsKeyword.Keyword),
		LPAR:   MustFormatKeyword(opts, throws.LParKeyword.Keyword),
		Fields: args,
		RPAR:   MustFormatKeyword(opts, throws.RParKeyword.Keyword),
	}

	return MustFormat(throwTpl, f)

}

func needAddtionalLineForFuncs(preNode, curNode parser.Node) bool {
	if preNode == nil {
		return false
	}

	curFunc := curNode.(*parser.Function)

	var curStartLine int
	if len(curFunc.Comments) > 0 {
		curStartLine = curFunc.Comments[0].Pos().Line
	} else {
		if curFunc.FunctionType != nil {
			curStartLine = curFunc.FunctionType.Pos().Line
		} else if curFunc.Void != nil {
			curStartLine = curFunc.Void.Pos().Line
		} else {
			curStartLine = curFunc.Name.Pos().Line
		}
	}

	return curStartLine-preNode.End().Line > 1
}
