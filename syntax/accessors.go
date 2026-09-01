package syntax

// Typed accessors over the document's ordered node list. The LSP consumes
// these heavily; the lists are rebuilt on each call, which is cheap for
// typical thrift files.

// Includes returns the thrift include headers in source order.
func (d *Document) Includes() []*Include {
	var out []*Include

	for _, n := range d.Nodes {
		if v, ok := n.(*Include); ok {
			out = append(out, v)
		}
	}

	return out
}

// CPPIncludes returns the cpp_include headers in source order.
func (d *Document) CPPIncludes() []*CPPInclude {
	var out []*CPPInclude

	for _, n := range d.Nodes {
		if v, ok := n.(*CPPInclude); ok {
			out = append(out, v)
		}
	}

	return out
}

// Namespaces returns the namespace headers in source order.
func (d *Document) Namespaces() []*Namespace {
	var out []*Namespace

	for _, n := range d.Nodes {
		if v, ok := n.(*Namespace); ok {
			out = append(out, v)
		}
	}

	return out
}

// Structs returns the struct declarations in source order.
func (d *Document) Structs() []*Struct {
	var out []*Struct

	for _, n := range d.Nodes {
		if v, ok := n.(*Struct); ok && v.Kind == StructDecl {
			out = append(out, v)
		}
	}

	return out
}

// Unions returns the union declarations in source order.
func (d *Document) Unions() []*Struct {
	var out []*Struct

	for _, n := range d.Nodes {
		if v, ok := n.(*Struct); ok && v.Kind == UnionDecl {
			out = append(out, v)
		}
	}

	return out
}

// Exceptions returns the exception declarations in source order.
func (d *Document) Exceptions() []*Struct {
	var out []*Struct

	for _, n := range d.Nodes {
		if v, ok := n.(*Struct); ok && v.Kind == ExceptionDecl {
			out = append(out, v)
		}
	}

	return out
}

// Enums returns the enum declarations in source order.
func (d *Document) Enums() []*Enum {
	var out []*Enum

	for _, n := range d.Nodes {
		if v, ok := n.(*Enum); ok {
			out = append(out, v)
		}
	}

	return out
}

// Services returns the service declarations in source order.
func (d *Document) Services() []*Service {
	var out []*Service

	for _, n := range d.Nodes {
		if v, ok := n.(*Service); ok {
			out = append(out, v)
		}
	}

	return out
}

// Consts returns the const declarations in source order.
func (d *Document) Consts() []*Const {
	var out []*Const

	for _, n := range d.Nodes {
		if v, ok := n.(*Const); ok {
			out = append(out, v)
		}
	}

	return out
}

// Typedefs returns the typedef declarations in source order.
func (d *Document) Typedefs() []*Typedef {
	var out []*Typedef

	for _, n := range d.Nodes {
		if v, ok := n.(*Typedef); ok {
			out = append(out, v)
		}
	}

	return out
}

// FieldListKind identifies the declaration a field list belongs to.
type FieldListKind uint8

const (
	StructFields FieldListKind = iota
	UnionFields
	ExceptionFields
	FunctionArgs // service function arguments
	ThrowsFields // a service function's throws clause
)

// WalkFieldLists visits the field lists of every struct, union,
// exception, service function argument, and throws clause in document
// order.
func (d *Document) WalkFieldLists(fn func(fields []*Field, kind FieldListKind)) {
	for _, st := range d.Structs() {
		fn(st.Fields, StructFields)
	}

	for _, union := range d.Unions() {
		fn(union.Fields, UnionFields)
	}

	for _, excep := range d.Exceptions() {
		fn(excep.Fields, ExceptionFields)
	}

	for _, svc := range d.Services() {
		for _, fnx := range svc.Functions {
			fn(fnx.Args, FunctionArgs)

			if fnx.Throws != nil {
				fn(fnx.Throws.Fields, ThrowsFields)
			}
		}
	}
}

// EachStructuredAnnotation visits every structured annotation in the
// document in source order: the ones leading definitions, functions and
// their arguments and throws entries, and struct fields. Unlike legacy
// annotation groups, structured annotations are tree nodes and are reached
// by Walk; this accessor exists for consumers that want the flat list.
func (d *Document) EachStructuredAnnotation(fn func(*StructuredAnnotation)) {
	visitFields := func(fields []*Field) {
		for _, f := range fields {
			for _, sa := range f.Structured {
				fn(sa)
			}
		}
	}

	for _, n := range d.Nodes {
		switch v := n.(type) {
		case *Namespace:
			for _, sa := range v.Structured {
				fn(sa)
			}
		case *Const:
			for _, sa := range v.Structured {
				fn(sa)
			}
		case *Typedef:
			for _, sa := range v.Structured {
				fn(sa)
			}
		case *Enum:
			for _, sa := range v.Structured {
				fn(sa)
			}
		case *Struct:
			for _, sa := range v.Structured {
				fn(sa)
			}

			visitFields(v.Fields)
		case *Service:
			for _, sa := range v.Structured {
				fn(sa)
			}

			for _, fnx := range v.Functions {
				for _, sa := range fnx.Structured {
					fn(sa)
				}

				visitFields(fnx.Args)

				if fnx.Throws != nil {
					visitFields(fnx.Throws.Fields)
				}
			}
		}
	}
}

// EachAnnotation visits every annotation group attached to any node of
// the document: namespaces, typedefs, structs, fields, enum values,
// services, functions, arguments, throws members, and container types.
//
// Annotation groups are not tree children — they decorate nodes the way
// trivia decorates tokens — so a plain tree walk does not reach them;
// consumers must come here instead.
func (d *Document) EachAnnotation(fn func(*Annotations)) {
	visit := func(a *Annotations) {
		if a != nil {
			fn(a)
		}
	}

	var visitType func(t *FieldType)

	visitType = func(t *FieldType) {
		if t == nil {
			return
		}

		visit(t.Annotations)
		visitType(t.KeyType)
		visitType(t.ValueType)
	}

	for _, n := range d.Nodes {
		switch v := n.(type) {
		case *Namespace:
			visit(v.Annotations)
		case *Typedef:
			visit(v.Annotations)
			visitType(v.Type)
		case *Const:
			visitType(v.Type)
		case *Enum:
			visit(v.Annotations)

			for _, ev := range v.Values {
				visit(ev.Annotations)
			}
		case *Struct:
			visit(v.Annotations)

			for _, f := range v.Fields {
				visit(f.Annotations)
				visitType(f.Type)
			}
		case *Service:
			visit(v.Annotations)

			for _, fnx := range v.Functions {
				visit(fnx.Annotations)
				visitType(fnx.Type)

				for _, a := range fnx.Args {
					visit(a.Annotations)
					visitType(a.Type)
				}

				if fnx.Throws != nil {
					for _, f := range fnx.Throws.Fields {
						visit(f.Annotations)
						visitType(f.Type)
					}
				}
			}
		}
	}
}
