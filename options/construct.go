package options

// Construct identifies one formatting construct that per-construct options
// apply to: the container bodies (structs, unions, exceptions, enums,
// arguments, throws) and the collection types (lists, maps, sets).
type Construct int

const (
	ConstructStruct Construct = iota
	ConstructUnion
	ConstructException
	ConstructEnum
	ConstructArguments
	ConstructThrows
	ConstructList
	ConstructMap
	ConstructSet
)

// PerConstruct holds one option value per construct. The JSON tags make the
// per-construct option maps config-compatible ("structs", "arguments", ...),
// so the options layer and the CLI share this single source of truth.
type PerConstruct[T any] struct {
	Structs    T `json:"structs"`
	Unions     T `json:"unions"`
	Exceptions T `json:"exceptions"`
	Enums      T `json:"enums"`
	Arguments  T `json:"arguments"`
	Throws     T `json:"throws"`
	Lists      T `json:"lists"`
	Maps       T `json:"maps"`
	Sets       T `json:"sets"`
}

// Get returns the value for the construct.
func (p PerConstruct[T]) Get(c Construct) T {
	switch c {
	case ConstructUnion:
		return p.Unions
	case ConstructException:
		return p.Exceptions
	case ConstructEnum:
		return p.Enums
	case ConstructArguments:
		return p.Arguments
	case ConstructThrows:
		return p.Throws
	case ConstructList:
		return p.Lists
	case ConstructMap:
		return p.Maps
	case ConstructSet:
		return p.Sets
	}

	return p.Structs
}

// Set assigns the value for the construct.
func (p *PerConstruct[T]) Set(c Construct, v T) {
	switch c {
	case ConstructUnion:
		p.Unions = v
	case ConstructException:
		p.Exceptions = v
	case ConstructEnum:
		p.Enums = v
	case ConstructArguments:
		p.Arguments = v
	case ConstructThrows:
		p.Throws = v
	case ConstructList:
		p.Lists = v
	case ConstructMap:
		p.Maps = v
	case ConstructSet:
		p.Sets = v
	default:
		p.Structs = v
	}
}

// AllConstructs lists every construct, in config order.
var AllConstructs = []Construct{
	ConstructStruct, ConstructUnion, ConstructException,
	ConstructEnum, ConstructArguments, ConstructThrows,
	ConstructList, ConstructMap, ConstructSet,
}

// String returns the config key of the construct.
func (c Construct) String() string {
	switch c {
	case ConstructUnion:
		return "unions"
	case ConstructException:
		return "exceptions"
	case ConstructEnum:
		return "enums"
	case ConstructArguments:
		return "arguments"
	case ConstructThrows:
		return "throws"
	case ConstructList:
		return "lists"
	case ConstructMap:
		return "maps"
	case ConstructSet:
		return "sets"
	}

	return "structs"
}
