package syntax

import "strings"

// This file defines the abstract syntax tree produced by the parser.
//
// Every node spans a contiguous range of tokens in the Document's token
// stream: TokStart/TokEnd are inclusive indices into Document.Tokens.
// Comments and blank-line information live on those tokens as trivia, so
// the tree itself stays free of formatting concerns.

// Node is any AST node. All nodes embed nodeBase, which provides the token
// range and seals the interface to this package.
type Node interface {
	TokStart() int
	TokEnd() int
	isNode()
}

type nodeBase struct {
	first, last int // inclusive token indices
}

func (b nodeBase) TokStart() int { return b.first }
func (b nodeBase) TokEnd() int   { return b.last }
func (nodeBase) isNode()         {}

// Identifier is a name: a plain identifier token.
type Identifier struct {
	nodeBase
	Text string
}

// FieldTypeKind discriminates the forms a type reference can take.
type FieldTypeKind uint8

const (
	TypeBase FieldTypeKind = iota
	TypeIdent
	TypeMap
	TypeList
	TypeSet
)

// FieldType is a type reference: a base type keyword, a named identifier, or
// a container (map/list/set). Base and container types may carry
// annotations; identifier types may not, matching the compiler.
type FieldType struct {
	nodeBase
	Kind FieldTypeKind

	Base      TokenKind // TypeBase: the base type keyword
	Ident     *Identifier
	KeyType   *FieldType // TypeMap: key type
	ValueType *FieldType // TypeMap/TypeList/TypeSet: value or element type
	CPPType   *Token     // optional cpp_type "..." literal on containers

	Annotations *Annotations
}

// ConstValueKind discriminates the forms a constant value can take.
type ConstValueKind uint8

const (
	ValueInt ConstValueKind = iota
	ValueDouble
	ValueString
	ValueIdent
	ValueList
	ValueMap
)

// ConstMapEntry is one key/value pair of a map constant. Order is preserved.
type ConstMapEntry struct {
	Key   *ConstValue
	Value *ConstValue
}

// ConstValue is a constant value: a scalar (int, double, string, identifier,
// true/false), a list, or a map. Scalar values keep their raw source text in
// Text (e.g. "0x1F", "-1.5e-3") so formatting is lossless.
type ConstValue struct {
	nodeBase
	Kind ConstValueKind
	Text string // raw source text of scalar values

	List []*ConstValue
	Map  []ConstMapEntry
}

// Include is a thrift include: include "path".
type Include struct {
	nodeBase
	Path *Token // the path string literal
}

// PathText returns the include path without its quotes. The token keeps
// the raw literal text, including the surrounding quotes.
func (i *Include) PathText() string {
	if i == nil || i.Path == nil {
		return ""
	}

	return strings.Trim(i.Path.Text, "\"'")
}

// CPPInclude is a C++ include: cpp_include "path".
type CPPInclude struct {
	nodeBase
	Path *Token // the path string literal
}

// Namespace declares a namespace: namespace <scope> <name>.
type Namespace struct {
	nodeBase
	Scope *Token // scope identifier or the '*' token
	Name  *Identifier

	Annotations *Annotations
}

// Const declares a constant: const <type> <name> = <value>.
type Const struct {
	nodeBase
	Type  *FieldType
	Name  *Identifier
	Value *ConstValue
	Sep   TokenKind // trailing , or ; (0 if none)
}

// Typedef declares a type alias: typedef <type> <name>.
type Typedef struct {
	nodeBase
	Type *FieldType
	Name *Identifier
	Sep  TokenKind

	Annotations *Annotations
}

// EnumValue is one enum member: <name> [= <int>].
type EnumValue struct {
	nodeBase
	Name  *Identifier
	Value *Token // the optional int token; nil means auto-incremented
	Sep   TokenKind

	Annotations *Annotations
}

// Enum declares an enum: enum <name> { <values> }.
type Enum struct {
	nodeBase
	Name   *Identifier
	Values []*EnumValue

	Annotations *Annotations
}

// StructKind distinguishes struct, union, and exception declarations.
// It is the token kind of the leading keyword.
type StructKind = TokenKind

const (
	StructDecl    StructKind = TokenStruct
	UnionDecl     StructKind = TokenUnion
	ExceptionDecl StructKind = TokenException
)

// Struct is a struct, union, or exception: <kind> <name> { <fields> }.
type Struct struct {
	nodeBase
	Kind   StructKind
	Name   *Identifier
	Fields []*Field

	Annotations *Annotations
}

// Throws is a function's throws clause: throws ( <fields> ).
type Throws struct {
	nodeBase
	Fields []*Field
}

// Function is a service method:
// [oneway] <type|void> <name> ( <args> ) [throws ( <exceptions> )].
type Function struct {
	nodeBase
	Oneway *Token // oneway/async keyword, nil for synchronous functions
	Type   *FieldType
	Void   *Token // the void keyword; Type is nil when Void is set
	Name   *Identifier
	Args   []*Field
	Throws *Throws
	Sep    TokenKind

	Annotations *Annotations
}

// Service declares a service: service <name> [extends <base>] { <functions> }.
type Service struct {
	nodeBase
	Name      *Identifier
	Extends   *Identifier // optional base service
	Functions []*Function

	Annotations *Annotations
}

// Field is a struct field, function argument, or throws entry:
// [<id>:] [required|optional] <type> [&] <name> [= <value>].
type Field struct {
	nodeBase
	FieldID   *Token // the optional id int token; nil means implicit
	Req       TokenKind
	Type      *FieldType
	Reference bool // & prefix (field reference)
	Name      *Identifier
	Value     *ConstValue
	Sep       TokenKind

	Annotations *Annotations
}

// Annotation is one entry of an annotation group: <name> [= "value"].
// A bare <name> is legal and means an implicit value of "1".
type Annotation struct {
	nodeBase
	Name  *Identifier
	Value *Token // the optional string literal; nil means bare
	Sep   TokenKind
}

// Annotations is a parenthesized annotation group: ( <annotations> ).
type Annotations struct {
	nodeBase
	Items []*Annotation
}

// Document is a parsed thrift file: its token stream and the top-level nodes
// in source order. It implements Node with a range spanning the whole file.
type Document struct {
	Tokens []Token
	Nodes  []Node
}

func (d *Document) TokStart() int { return 0 }
func (d *Document) TokEnd() int   { return len(d.Tokens) - 1 }
func (*Document) isNode()         {}
