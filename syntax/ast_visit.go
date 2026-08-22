package syntax

import "fmt"

// SearchNodePathByPosition returns the path of nodes containing pos, from
// the document root to the innermost node. The LSP uses the deepest node to
// decide what the cursor is on and its ancestors for context.
func (d *Document) SearchNodePathByPosition(pos Position) []Node {
	var path []Node
	d.searchNodePath(d, pos, &path)

	return path
}

func (d *Document) searchNodePath(root Node, pos Position, path *[]Node) {
	if !d.Contains(root, pos) {
		return
	}

	*path = append(*path, root)
	for _, child := range nodeChildren(root) {
		d.searchNodePath(child, pos, path)
	}
}

// nodeChildren enumerates the child nodes of n. Identifiers appear as leaf
// nodes, so the deepest node on a name is the identifier itself; the parent
// path element identifies its role (a field name, a type reference, a
// definition name, and so on).
//
// The switch must stay exhaustive over the Node set: position search and
// Walk route every traversal through here, so an unhandled node type would
// silently truncate traversals. The default case panics instead, because a
// missed case is a bug in this package, not bad input.
func nodeChildren(n Node) []Node {
	switch v := n.(type) {
	case *Document:
		return v.Nodes
	case *Const:
		return []Node{v.Type, v.Name, v.Value}
	case *Typedef:
		return []Node{v.Type, v.Name}
	case *Enum:
		out := []Node{v.Name}
		for _, value := range v.Values {
			out = append(out, value)
		}

		return out
	case *EnumValue:
		return []Node{v.Name}
	case *Struct:
		out := []Node{v.Name}
		for _, field := range v.Fields {
			out = append(out, field)
		}

		return out
	case *Service:
		out := []Node{v.Name}
		if v.Extends != nil {
			out = append(out, v.Extends)
		}

		for _, fn := range v.Functions {
			out = append(out, fn)
		}

		return out
	case *Function:
		out := []Node{v.Name}
		if v.Type != nil {
			out = append(out, v.Type)
		}

		for _, arg := range v.Args {
			out = append(out, arg)
		}

		if v.Throws != nil {
			out = append(out, v.Throws)
		}

		return out
	case *Throws:
		out := make([]Node, 0, len(v.Fields))
		for _, field := range v.Fields {
			out = append(out, field)
		}

		return out
	case *Field:
		out := []Node{v.Type, v.Name}
		if v.Value != nil {
			out = append(out, v.Value)
		}

		return out
	case *FieldType:
		var out []Node
		if v.Ident != nil {
			out = append(out, v.Ident)
		}

		if v.KeyType != nil {
			out = append(out, v.KeyType)
		}

		if v.ValueType != nil {
			out = append(out, v.ValueType)
		}

		return out
	case *ConstValue:
		var out []Node
		for _, item := range v.List {
			out = append(out, item)
		}

		for _, entry := range v.Map {
			out = append(out, entry.Key, entry.Value)
		}

		return out
	case *Namespace:
		return []Node{v.Name}
	case *Include, *CPPInclude, *Identifier:
		return nil
	case *Annotation, *Annotations:
		// Annotations stay opaque: their names and values are not part of
		// any node path.
		return nil
	default:
		panic(fmt.Sprintf("syntax: nodeChildren missing case for %T", n))
	}
}

// Walk visits n and its descendants depth-first in source order, preorder:
// n first, then each child's subtree. visit receives every node and returns
// whether to descend into that node's children. Use it to collect facts
// across a document instead of writing another hand-rolled recursion; it
// inherits nodeChildren's exhaustiveness, so new node kinds cannot be
// silently skipped.
func Walk(n Node, visit func(Node) bool) {
	if !visit(n) {
		return
	}

	for _, child := range nodeChildren(n) {
		Walk(child, visit)
	}
}
