package format

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joyme123/thrift-ls/parser"
)

type fmtContext struct {
	// preNode record previous print node. we can use preNode as print context
	// if preNodex is const or typdef, and current node is const or typedef, '\n' should be ignore
	preNode parser.Node
}

func FormatDocument(doc *parser.Document, opts Options) (string, error) {
	return FormatDocumentWithValidation(doc, opts, false)
}

func FormatDocumentWithValidation(doc *parser.Document, opts Options, selfValidation bool) (string, error) {
	if doc.ChildrenBadNode() {
		return "", BadNodeError
	}

	buf := bytes.NewBuffer(nil)

	fmtCtx := &fmtContext{}

	writeBuf := func(node parser.Node, addtionalLine bool) {
		if addtionalLine {
			if len(buf.Bytes()) > 0 && buf.Bytes()[buf.Len()-1] != '\n' {
				// if preNode doesn't have \n at end of line, set \n for it
				buf.WriteString("\n")
			}
			buf.WriteString("\n")
		}

		switch node.Type() {
		case "Include":
			buf.WriteString(MustFormatInclude(node.(*parser.Include), opts))
		case "CPPInclude":
			buf.WriteString(MustFormatCPPInclude(node.(*parser.CPPInclude), opts))
		case "Namespace":
			buf.WriteString(MustFormatNamespace(node.(*parser.Namespace), opts))
		case "Struct":
			buf.WriteString(MustFormatStruct(node.(*parser.Struct), opts))
		case "Union":
			buf.WriteString(MustFormatUnion(node.(*parser.Union), opts))
		case "Exception":
			buf.WriteString(MustFormatException(node.(*parser.Exception), opts))
		case "Service":
			buf.WriteString(MustFormatService(node.(*parser.Service), opts))
		case "Typedef":
			buf.WriteString(MustFormatTypedef(node.(*parser.Typedef), opts))
		case "Const":
			buf.WriteString(MustFormatConst(node.(*parser.Const), opts))
		case "Enum":
			buf.WriteString(MustFormatEnum(node.(*parser.Enum), opts))
		}

	}

	for _, node := range doc.Nodes {
		addtionalLine := needAddtionalLineInDocument(fmtCtx.preNode, node)
		writeBuf(node, addtionalLine)
		fmtCtx.preNode = node
	}

	if len(doc.Comments) > 0 {
		buf.WriteString(MustFormatComments(opts, doc.Comments, "", ""))
	}
	res := buf.String()

	res = strings.TrimSpace(res)

	if selfValidation {
		psr := parser.PEGParser{}
		formattedAst, err := psr.Parse("formated.thrift", []byte(res))
		if err != nil {
			return "", fmt.Errorf("format error: format result failed to parse, error msg: %v. Please report bug to author at https://github.com/joyme123/thrift-ls/issues", err)
		}

		if !doc.Equals(formattedAst) {
			return "", fmt.Errorf("format error: format result failed to pass self validation. Please report bug to author at https://github.com/joyme123/thrift-ls/issues")
		}
	}

	return res, nil
}

// FormatDocumentWithValidationFull formats a document with self-validation using include resolution.
// When includePaths is provided and non-empty, self-validation uses ParseRecursively to resolve includes.
// When includePaths is empty, falls back to plain Parse for backward compatibility.
func FormatDocumentWithValidationFull(doc *parser.Document, opts Options, selfValidation bool, includePaths []string, currentFile string) (string, error) {
	if doc.ChildrenBadNode() {
		return "", BadNodeError
	}

	buf := bytes.NewBuffer(nil)

	fmtCtx := &fmtContext{}

	writeBuf := func(node parser.Node, addtionalLine bool) {
		if addtionalLine {
			if len(buf.Bytes()) > 0 && buf.Bytes()[buf.Len()-1] != '\n' {
				// if preNode doesn't have \n at end of line, set \n for it
				buf.WriteString("\n")
			}
			buf.WriteString("\n")
		}

		switch node.Type() {
		case "Include":
			buf.WriteString(MustFormatInclude(node.(*parser.Include), opts))
		case "CPPInclude":
			buf.WriteString(MustFormatCPPInclude(node.(*parser.CPPInclude), opts))
		case "Namespace":
			buf.WriteString(MustFormatNamespace(node.(*parser.Namespace), opts))
		case "Struct":
			buf.WriteString(MustFormatStruct(node.(*parser.Struct), opts))
		case "Union":
			buf.WriteString(MustFormatUnion(node.(*parser.Union), opts))
		case "Exception":
			buf.WriteString(MustFormatException(node.(*parser.Exception), opts))
		case "Service":
			buf.WriteString(MustFormatService(node.(*parser.Service), opts))
		case "Typedef":
			buf.WriteString(MustFormatTypedef(node.(*parser.Typedef), opts))
		case "Const":
			buf.WriteString(MustFormatConst(node.(*parser.Const), opts))
		case "Enum":
			buf.WriteString(MustFormatEnum(node.(*parser.Enum), opts))
		}

	}

	for _, node := range doc.Nodes {
		addtionalLine := needAddtionalLineInDocument(fmtCtx.preNode, node)
		writeBuf(node, addtionalLine)
		fmtCtx.preNode = node
	}

	if len(doc.Comments) > 0 {
		buf.WriteString(MustFormatComments(opts, doc.Comments, "", ""))
	}
	res := buf.String()

	res = strings.TrimSpace(res)

	if selfValidation {
		psr := parser.PEGParser{}
		var formattedAst *parser.Document

		if len(includePaths) > 0 && currentFile != "" {
			// Try with include resolution
			results := psr.ParseRecursively("formatted.thrift", []byte(res), 0, createIncludeCall(includePaths, currentFile))
			if len(results) > 0 && results[0].Doc != nil && len(results[0].Errors) == 0 {
				formattedAst = results[0].Doc
			}
		}

		// Fall back to plain parse if include resolution failed
		if formattedAst == nil {
			var errs []error
			formattedAst, errs = psr.Parse("formatted.thrift", []byte(res))
			if len(errs) > 0 {
				return "", fmt.Errorf("format error: format result failed to parse: %v", errs)
			}
		}

		if formattedAst != nil && !doc.Equals(formattedAst) {
			return "", fmt.Errorf("format error: format result failed to pass self validation")
		}
	}

	if opts.TrailingNewline && !strings.HasSuffix(res, "\n") {
		res += "\n"
	}

	return res, nil
}

// createIncludeCall creates a parser.IncludeCall function that resolves includes
// using the provided include paths and falls back to relative resolution.
func createIncludeCall(includePaths []string, currentFile string) parser.IncludeCall {
	return func(include string) (filename string, content []byte, err error) {
		// Try include paths first
		for _, ip := range includePaths {
			candidatePath := filepath.Join(ip, include)
			if _, statErr := os.Stat(candidatePath); statErr == nil {
				content, err = os.ReadFile(candidatePath)
				return candidatePath, content, err
			}
		}

		// Fall back to relative resolution from current file
		basePath := filepath.Dir(currentFile)
		resolvedPath := filepath.Join(basePath, include)
		content, err = os.ReadFile(resolvedPath)
		return resolvedPath, content, err
	}
}

var (
	header = map[string]struct{}{
		"Include":    {},
		"CPPInclude": {},
		"Namespace":  {},
	}
	onelineDefinition = map[string]struct{}{
		"Const":   {},
		"Typedef": {},
	}
	multiLineDefinition = map[string]struct{}{
		"Struct":    {},
		"Union":     {},
		"Exception": {},
		"Service":   {},
		"Typedef":   {},
		"Const":     {},
		"Enum":      {},
	}
)

func isHeader(node parser.Node) bool {
	_, ok := header[node.Type()]
	return ok
}

func isOneLineDefinition(node parser.Node) bool {
	_, ok := onelineDefinition[node.Type()]
	return ok
}

func isMultiLineDefinition(node parser.Node) bool {
	_, ok := multiLineDefinition[node.Type()]
	return ok
}

func needAddtionalLineInDocument(preNode parser.Node, currentNode parser.Node) bool {
	if preNode == nil {
		return false
	}

	if isHeader(preNode) && isHeader(currentNode) {
		if preNode.Type() == currentNode.Type() {
			if lineDistance(preNode, currentNode) > 1 {
				return true
			}
			return false
		}
		return true
	}

	if isOneLineDefinition(preNode) && isOneLineDefinition(currentNode) {
		// if preNode and currentNode has one or more empty lines between them, we should reserve
		// one empty line
		if lineDistance(preNode, currentNode) > 1 {
			return true
		}
		return false
	}

	return true
}
