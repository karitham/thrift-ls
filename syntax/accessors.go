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
