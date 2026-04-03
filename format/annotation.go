package format

import (
	"bytes"
	"fmt"

	"github.com/joyme123/thrift-ls/parser"
)

func MustFormatAnnotations(annotations *parser.Annotations, opts Options) string {
	buf := bytes.NewBuffer(nil)

	buf.WriteString(MustFormatKeyword(opts, annotations.LParKeyword.Keyword))

	var preNode parser.Node
	preNode = annotations.LParKeyword

	indent := ""
	isNewLine := false

	for i, anno := range annotations.Annotations {
		if lineDistance(preNode, annotations.Annotations[i]) >= 1 {
			buf.WriteString("\n")
			isNewLine = true
			indent = opts.GetIndent() + opts.GetIndent()
		}
		buf.WriteString(MustFormatAnnotation(anno, opts, i == len(annotations.Annotations)-1, i == 0, indent, isNewLine))
		preNode = annotations.Annotations[i]
		isNewLine = false
		indent = ""
	}

	if lineDistance(preNode, annotations.RParKeyword) >= 1 {
		buf.WriteString("\n")
		buf.WriteString(opts.GetIndent())
	}
	buf.WriteString(MustFormatKeyword(opts, annotations.RParKeyword.Keyword))

	return buf.String()
}

func MustFormatAnnotation(anno *parser.Annotation, opts Options, isLast bool, isFirst bool, indent string, isNewLine bool) string {
	sep := ""
	if (!isLast) && anno.ListSeparatorKeyword != nil {
		sep = MustFormatKeyword(opts, anno.ListSeparatorKeyword.Keyword)
	}

	space := ""
	if (!isFirst) && (!isNewLine) {
		space = " "
	}

	// a = "xxxx",
	return fmt.Sprintf("%s%s %s %s%s", space, MustFormatIdentifier(opts, anno.Identifier, indent), MustFormatKeyword(opts, anno.EqualKeyword.Keyword), MustFormatLiteral(opts, anno.Value, ""), sep)
}
