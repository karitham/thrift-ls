package options

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
)

// checkExactKeys rejects document keys that no field spells exactly.
// encoding/json otherwise matches keys case-insensitively before
// DisallowUnknownFields can reject them. Nested structs are checked too.
func checkExactKeys(doc []byte, v any) error {
	return walkExactKeys(doc, reflect.TypeOf(v), "")
}

// walkExactKeys checks one fragment against the struct json decodes it into.
// Non-structs and non-objects are left to the decoder.
func walkExactKeys(doc []byte, t reflect.Type, path string) error {
	t = structOf(t)
	if t.Kind() != reflect.Struct {
		return nil
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(doc, &m); err != nil {
		return nil
	}

	for _, k := range slices.Sorted(maps.Keys(m)) {
		f, ok := exactField(t, k)
		if !ok {
			name := k
			if path != "" {
				name = path + "." + k
			}

			return fmt.Errorf("unknown key %q", name)
		}

		if err := walkExactKeys(m[k], f.Type, path+"."+k); err != nil {
			return err
		}
	}

	return nil
}

// exactField finds the field json decodes key into: tags, promoted embedded
// fields, and Go names all count; json:"-" fields do not.
func exactField(t reflect.Type, key string) (reflect.StructField, bool) {
	for f := range t.Fields() {
		f := f
		if !f.IsExported() {
			continue
		}

		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}

		if name == "" {
			if f.Anonymous && structOf(f.Type).Kind() == reflect.Struct {
				if sf, ok := exactField(structOf(f.Type), key); ok {
					return sf, true
				}

				continue
			}

			name = f.Name
		}

		if name == key {
			return f, true
		}
	}

	return reflect.StructField{}, false
}

func structOf(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	return t
}
