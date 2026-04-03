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

// formatConfig holds internal behavioral options for document formatting.
// These are not user-facing formatting preferences but control validation behavior.
type formatConfig struct {
	selfValidation bool
	includePaths   []string
	currentFile    string
}

// FormatDocument formats a Thrift document according to the provided options.
// This is the basic formatting function without self-validation.
func FormatDocument(doc *parser.Document, opts Options) (string, error) {
	return formatDocument(doc, opts, formatConfig{})
}

// FormatDocumentWithValidation formats a Thrift document with self-validation enabled.
// The formatted output is parsed back to ensure it's valid.
func FormatDocumentWithValidation(doc *parser.Document, opts Options) (string, error) {
	return formatDocument(doc, opts, formatConfig{selfValidation: true})
}

// FormatDocumentWithValidationFull formats a Thrift document with full validation support.
// It includes paths for resolving includes and a current file path for relative resolution.
// This is useful when the document contains includes that need to be resolved during validation.
func FormatDocumentWithValidationFull(doc *parser.Document, opts Options, includePaths []string, currentFile string) (string, error) {
	return formatDocument(doc, opts, formatConfig{
		selfValidation: true,
		includePaths:   includePaths,
		currentFile:    currentFile,
	})
}

// formatDocument is the internal implementation that all public functions delegate to.
func formatDocument(doc *parser.Document, opts Options, cfg formatConfig) (string, error) {
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

	if opts.TrailingNewline && !strings.HasSuffix(res, "\n") {
		res += "\n"
	}

	if cfg.selfValidation {
		if err := validateFormattedDocument(doc, res, cfg); err != nil {
			return "", err
		}
	}

	return res, nil
}

// validateFormattedDocument parses the formatted result and validates it against the original.
// It uses ParseRecursively when includePaths is non-empty and currentFile is set;
// otherwise it falls back to plain Parse.
func validateFormattedDocument(originalDoc *parser.Document, formatted string, cfg formatConfig) error {
	psr := parser.PEGParser{}

	var formattedAst *parser.Document

	if len(cfg.includePaths) > 0 && cfg.currentFile != "" {
		results := psr.ParseRecursively("formatted.thrift", []byte(formatted), 0, createIncludeCall(cfg.includePaths, cfg.currentFile))
		if len(results) > 0 && results[0].Doc != nil && len(results[0].Errors) == 0 {
			formattedAst = results[0].Doc
		}
	}

	if formattedAst == nil {
		var errs []error
		formattedAst, errs = psr.Parse("formatted.thrift", []byte(formatted))
		if len(errs) > 0 {
			return fmt.Errorf("format error: format result failed to parse: %v", errs)
		}
	}

	if formattedAst != nil && !originalDoc.Equals(formattedAst) {
		return fmt.Errorf("format error: format result failed to pass self validation")
	}

	return nil
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

// constHasMultiLineValue returns true if the const has a list with multiple
// items or a map value, which should be treated as a multi-line definition
func constHasMultiLineValue(cst *parser.Const) bool {
	if cst.Value == nil {
		return false
	}
	if cst.Value.TypeName == "map" {
		return true
	}
	if cst.Value.TypeName == "list" {
		values := cst.Value.Value.([]*parser.ConstValue)
		return len(values) > 1
	}
	return false
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
		// Consts with list/map values should be treated as multi-line
		if preNode.Type() == "Const" && constHasMultiLineValue(preNode.(*parser.Const)) {
			return true
		}
		return false
	}

	return true
}
