package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestContractSchemasMatchGoTypes(t *testing.T) {
	checkSchema(t, Request{}, "request.schema.json")
	checkSchema(t, Response{}, "response.schema.json")
	checkSchema(t, Report{}, "inspect-report.schema.json")
	checkSchema(t, Pack{}, "prepare-pack.schema.json")
}

func checkSchema(t *testing.T, value any, name string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "session", "v1", name))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err = json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if schema["additionalProperties"] != false {
		t.Fatalf("%s allows unknown fields", name)
	}
	defs, _ := schema["$defs"].(map[string]any)
	compareStruct(t, reflect.TypeOf(value), schema, defs)
}

func compareStruct(t *testing.T, typ reflect.Type, schema map[string]any, defs map[string]any) {
	t.Helper()
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("%s schema has no properties", typ.Name())
	}
	required := map[string]bool{}
	if raw, ok := schema["required"].([]any); ok {
		for _, item := range raw {
			required[item.(string)] = true
		}
	}
	seen := map[string]bool{}
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if field.PkgPath != "" {
			continue
		}
		tag := field.Tag.Get("json")
		if tag == "" || tag == "-" {
			t.Fatalf("%s.%s has no JSON name", typ.Name(), field.Name)
		}
		name := strings.Split(tag, ",")[0]
		seen[name] = true
		prop, ok := props[name].(map[string]any)
		if !ok {
			t.Fatalf("%s.%s is missing from the schema", typ.Name(), name)
		}
		optional := strings.Contains(tag, "omitempty")
		if optional == required[name] {
			t.Fatalf("%s.%s omitempty=%v required=%v", typ.Name(), name, optional, required[name])
		}
		if err := enumsAgree(field.Tag.Get("enum"), prop); err != nil {
			t.Fatalf("%s.%s: %v", typ.Name(), name, err)
		}
		ref, _ := prop["$ref"].(string)
		if ref == "" {
			if items, ok := prop["items"].(map[string]any); ok {
				ref, _ = items["$ref"].(string)
			}
		}
		if ref == "" {
			continue
		}
		defName := strings.TrimPrefix(ref, "#/$defs/")
		def, ok := defs[defName].(map[string]any)
		if !ok {
			t.Fatalf("%s.%s points at missing %s", typ.Name(), name, defName)
		}
		if def["additionalProperties"] != false {
			t.Fatalf("%s allows unknown fields", defName)
		}
		next := field.Type
		for next.Kind() == reflect.Pointer || next.Kind() == reflect.Slice {
			next = next.Elem()
		}
		if next.Kind() == reflect.Struct {
			compareStruct(t, next, def, defs)
		}
	}
	for name := range props {
		if !seen[name] {
			t.Fatalf("schema field %s is not in %s", name, typ.Name())
		}
	}
}

func TestRequestSchemaKeepsPromptExclusive(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "session", "v1", "request.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err = json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	clauses, ok := schema["allOf"].([]any)
	if !ok || len(clauses) != 1 {
		t.Fatalf("allOf: %#v", schema["allOf"])
	}
	clause, ok := clauses[0].(map[string]any)
	if !ok {
		t.Fatalf("clause: %#v", clauses[0])
	}
	condition, ok := clause["if"].(map[string]any)
	if !ok || !containsString(condition["required"], "prompt") {
		t.Fatalf("if: %#v", clause["if"])
	}
	prompt, _ := condition["properties"].(map[string]any)["prompt"].(map[string]any)
	if prompt["minLength"] != float64(1) {
		t.Fatalf("prompt condition: %#v", prompt)
	}
	accepted, ok := clause["then"].(map[string]any)["properties"].(map[string]any)
	if !ok || accepted["op"].(map[string]any)["const"] != "prepare" || accepted["query"].(map[string]any)["const"] != "" || accepted["terms"].(map[string]any)["maxItems"] != float64(0) {
		t.Fatalf("then: %#v", clause["then"])
	}
}

func containsString(value any, want string) bool {
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func enumsAgree(tag string, prop map[string]any) error {
	raw, has := prop["enum"].([]any)
	if (tag == "") == has {
		return errEnum(tag, raw)
	}
	if tag == "" {
		return nil
	}
	want := strings.Split(tag, ",")
	if len(want) != len(raw) {
		return errEnum(tag, raw)
	}
	for index, item := range want {
		if raw[index] != item {
			return errEnum(tag, raw)
		}
	}
	return nil
}

type enumMismatch struct {
	tag string
	raw []any
}

func errEnum(tag string, raw []any) error {
	return enumMismatch{tag: tag, raw: raw}
}

func (e enumMismatch) Error() string {
	return "enum " + e.tag
}
