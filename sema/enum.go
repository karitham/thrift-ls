package sema

import (
	"strconv"

	"github.com/karitham/thrift-ls/syntax"
)

// EnumMemberValue is an enum member with the value the compiler resolves
// for it: explicit constants parse as written (base-0), implicit members
// auto-increment (0 for the first member, one greater than the preceding
// member's value otherwise). Known is false when the preceding value is
// broken, so Value is unknowable until the next parseable constant settles
// the chain.
type EnumMemberValue struct {
	Member *syntax.EnumValue
	Value  int64
	Known  bool
}

// EnumMemberValues resolves every member of an enum to its on-wire value.
func EnumMemberValues(enum *syntax.Enum) []EnumMemberValue {
	var out []EnumMemberValue

	// A virtual value of -1 precedes the first member so the first
	// implicit member auto-increments to 0, mirroring the compiler.
	val, known := int64(-1), true

	for _, member := range enum.Values {
		if member.Value == nil {
			if known {
				val++
			}

			out = append(out, EnumMemberValue{Member: member, Value: val, Known: known})
			continue
		}

		v, err := strconv.ParseInt(member.Value.Text, 0, 64)
		if err != nil {
			out = append(out, EnumMemberValue{Member: member, Known: false})
			known = false
			continue
		}

		val, known = v, true
		out = append(out, EnumMemberValue{Member: member, Value: val, Known: true})
	}

	return out
}

// EnumImplicitValue is an enum member that lacks an explicit value, with
// the int constant the compiler auto-increments for it.
type EnumImplicitValue struct {
	Member *syntax.EnumValue
	Value  int64
	Known  bool // false when the preceding value is broken, so Value is unknowable
}

// EnumImplicitValues reports the members of an enum that carry no explicit
// value, together with the value the compiler would auto-increment: 0 for
// the first member, one greater than the preceding member's value
// otherwise. Members after an unparseable explicit constant report
// Known=false until the next parseable constant settles the chain.
func EnumImplicitValues(enum *syntax.Enum) []EnumImplicitValue {
	var out []EnumImplicitValue

	for _, mv := range EnumMemberValues(enum) {
		if mv.Member.Value != nil {
			continue
		}

		out = append(out, EnumImplicitValue{Member: mv.Member, Value: mv.Value, Known: mv.Known})
	}

	return out
}
