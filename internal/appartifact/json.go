// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package appartifact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

// jsonValue rejects duplicate keys, invalid UTF-8, excessive nesting, and trailing
// values before either Go's case-insensitive struct decoder or protobuf decoding.
func jsonValue(raw []byte) (any, error) {
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("invalid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := readValue(decoder, 0)
	if err != nil {
		return nil, err
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing JSON")
	}
	return value, nil
}

func readValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > 16 {
		return nil, fmt.Errorf("JSON nesting exceeds limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("invalid JSON")
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return token, nil
	}
	switch delimiter {
	case '{':
		value := map[string]any{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, fmt.Errorf("invalid JSON key")
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("invalid JSON key")
			}
			if _, exists := value[key]; exists {
				return nil, fmt.Errorf("duplicate JSON key %q", key)
			}
			value[key], err = readValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return nil, fmt.Errorf("invalid JSON object")
		}
		return value, nil
	case '[':
		value := []any{}
		for decoder.More() {
			item, err := readValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			value = append(value, item)
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return nil, fmt.Errorf("invalid JSON array")
		}
		return value, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter")
	}
}

func decodeStrict(raw []byte, out any) error {
	value, err := jsonValue(raw)
	if err != nil {
		return err
	}
	if err = validateShape(value, reflect.TypeOf(out).Elem()); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(out); err != nil {
		return fmt.Errorf("invalid field value: %w", err)
	}
	return nil
}

func validateShape(value any, typ reflect.Type) error {
	if value == nil {
		return fmt.Errorf("null fields are unsupported")
	}
	if typ.Kind() == reflect.Pointer {
		return validateShape(value, typ.Elem())
	}
	switch typ.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("expected JSON object")
		}
		fields := map[string]reflect.StructField{}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			tag := strings.Split(field.Tag.Get("json"), ",")
			fields[tag[0]] = field
			if len(tag) == 1 {
				if _, exists := object[tag[0]]; !exists {
					return fmt.Errorf("missing required field %q", tag[0])
				}
			}
		}
		for name, child := range object {
			field, exists := fields[name]
			if !exists {
				return fmt.Errorf("unknown field %q", name)
			}
			if err := validateShape(child, field.Type); err != nil {
				return fmt.Errorf("field %q: %w", name, err)
			}
		}
	case reflect.Map:
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("expected JSON object")
		}
		for _, child := range object {
			if err := validateShape(child, typ.Elem()); err != nil {
				return err
			}
		}
	case reflect.String:
		text, ok := value.(string)
		if !ok || text == "" {
			return fmt.Errorf("expected nonempty string")
		}
	case reflect.Int64:
		if _, ok := value.(json.Number); !ok {
			return fmt.Errorf("expected integer")
		}
	default:
		return fmt.Errorf("unsupported JSON shape")
	}
	return nil
}
