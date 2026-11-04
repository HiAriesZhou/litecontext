// Package strictjson decodes closed JSON contracts without duplicate keys,
// missing required fields, unknown fields, trailing values or unbounded nesting.
package strictjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

func Decode(data []byte, target any) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("invalid UTF-8 JSON")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	value, err := read(d, 0)
	if err != nil {
		return err
	}
	if _, err = d.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	t := reflect.TypeOf(target)
	if t == nil || t.Kind() != reflect.Pointer {
		return fmt.Errorf("decode target must be a pointer")
	}
	if err = shape(value, t.Elem()); err != nil {
		return err
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode(target)
}
func read(d *json.Decoder, depth int) (any, error) {
	if depth > 64 {
		return nil, fmt.Errorf("JSON nesting limit exceeded")
	}
	tok, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	switch delim {
	case '{':
		m := map[string]any{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return nil, e
			}
			key, ok := k.(string)
			if !ok {
				return nil, fmt.Errorf("invalid object key")
			}
			if _, ok = m[key]; ok {
				return nil, fmt.Errorf("duplicate JSON key %q", key)
			}
			v, e := read(d, depth+1)
			if e != nil {
				return nil, e
			}
			m[key] = v
		}
		_, err = d.Token()
		return m, err
	case '[':
		a := []any{}
		for d.More() {
			v, e := read(d, depth+1)
			if e != nil {
				return nil, e
			}
			a = append(a, v)
		}
		_, err = d.Token()
		return a, err
	}
	return nil, fmt.Errorf("unexpected JSON delimiter")
}
func shape(v any, t reflect.Type) error {
	if t.Kind() == reflect.Interface {
		return nil
	}
	if v == nil {
		return fmt.Errorf("null is not allowed in typed fields")
	}
	switch t.Kind() {
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("expected object")
		}
		known := map[string]bool{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := f.Tag.Get("json")
			if tag == "-" || f.PkgPath != "" {
				continue
			}
			parts := strings.Split(tag, ",")
			key := parts[0]
			if key == "" {
				key = f.Name
			}
			known[key] = true
			value, exists := m[key]
			if !exists {
				if !strings.Contains(tag, ",omitempty") {
					return fmt.Errorf("missing required field %q", key)
				}
				continue
			}
			if options := f.Tag.Get("enum"); options != "" {
				str, ok := value.(string)
				valid := false
				for _, option := range strings.Split(options, ",") {
					if str == option {
						valid = true
					}
				}
				if !ok || !valid {
					return fmt.Errorf("invalid enum field %s", key)
				}
			}
			if f.Tag.Get("nonempty") == "true" && value == "" {
				return fmt.Errorf("empty field %s", key)
			}
			if err := shape(value, f.Type); err != nil {
				return fmt.Errorf("field %s: %w", key, err)
			}
		}
		for key := range m {
			if !known[key] {
				return fmt.Errorf("unknown field %q", key)
			}
		}
	case reflect.Map:
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("expected object")
		}
		for _, value := range m {
			if err := shape(value, t.Elem()); err != nil {
				return err
			}
		}
	case reflect.Slice:
		a, ok := v.([]any)
		if !ok {
			return fmt.Errorf("expected array")
		}
		for _, value := range a {
			if err := shape(value, t.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}
