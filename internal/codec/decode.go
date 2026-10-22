package codec

import (
	"bytes"
	"encoding/json"
	"maps"
	"reflect"
	"strings"

	"github.com/temirlankasen/peerturn/internal/fault"
)

var rawType = reflect.TypeFor[json.RawMessage]()

// Decode bounds and validates JSON, rejecting duplicate keys and non-exact schema names.
func Decode(data []byte, max int, out any) error {
	value, err := parse(data, max)
	if err != nil {
		return err
	}
	target := reflect.TypeOf(out)
	if target == nil || target.Kind() != reflect.Pointer || reflect.ValueOf(out).IsNil() {
		return fault.New(fault.InvalidEncoding)
	}
	if !matches(value, target.Elem()) {
		return fault.New(fault.InvalidEncoding)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(out); err != nil {
		return fault.New(fault.InvalidEncoding)
	}
	return nil
}

func matches(value any, target reflect.Type) bool {
	if target == rawType {
		return true
	}
	if target.Kind() == reflect.Pointer {
		return value == nil || matches(value, target.Elem())
	}
	if value == nil {
		return target.Kind() == reflect.Map || target.Kind() == reflect.Slice || target.Kind() == reflect.Interface
	}
	switch target.Kind() {
	case reflect.Interface:
		return target.NumMethod() == 0
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return false
		}
		fields := schemaFields(target)
		for name, child := range object {
			field, ok := fields[name]
			if !ok || !matches(child, field) {
				return false
			}
		}
		return true
	case reflect.Map:
		object, ok := value.(map[string]any)
		if !ok || target.Key().Kind() != reflect.String {
			return false
		}
		for _, child := range object {
			if !matches(child, target.Elem()) {
				return false
			}
		}
		return true
	case reflect.Slice, reflect.Array:
		if target.Elem().Kind() == reflect.Uint8 {
			_, ok := value.(string)
			return ok && target.Kind() == reflect.Slice
		}
		array, ok := value.([]any)
		if !ok || (target.Kind() == reflect.Array && len(array) != target.Len()) {
			return false
		}
		for _, child := range array {
			if !matches(child, target.Elem()) {
				return false
			}
		}
		return true
	case reflect.String:
		if target == reflect.TypeFor[json.Number]() {
			_, ok := value.(json.Number)
			return ok
		}
		_, ok := value.(string)
		return ok
	case reflect.Bool:
		_, ok := value.(bool)
		return ok
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		number, ok := value.(json.Number)
		return ok && !strings.ContainsAny(string(number), ".eE")
	case reflect.Invalid, reflect.Uintptr, reflect.Float32, reflect.Float64,
		reflect.Complex64, reflect.Complex128, reflect.Chan, reflect.Func,
		reflect.Pointer, reflect.UnsafePointer:
		return false
	}
	return false
}

func schemaFields(target reflect.Type) map[string]reflect.Type {
	fields := make(map[string]reflect.Type)
	for field := range target.Fields() {
		if !field.IsExported() {
			continue
		}
		tag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if tag == "-" {
			continue
		}
		if field.Anonymous && tag == "" {
			child := field.Type
			if child.Kind() == reflect.Pointer {
				child = child.Elem()
			}
			if child.Kind() == reflect.Struct {
				maps.Copy(fields, schemaFields(child))
				continue
			}
		}
		if tag == "" {
			tag = field.Name
		}
		fields[tag] = field.Type
	}
	return fields
}
