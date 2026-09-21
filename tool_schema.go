package tinyagent

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// rawMessageType 用于识别 json.RawMessage，它需要特殊处理。
var rawMessageType = reflect.TypeOf(json.RawMessage(nil))

// schemaFromType 从 Go 类型生成 JSON Schema。
//
// 类型映射：
//
//	string           -> {"type":"string"}
//	bool             -> {"type":"boolean"}
//	整数类型          -> {"type":"integer"}
//	浮点类型          -> {"type":"number"}
//	slice / array    -> {"type":"array","items":...}
//	map[string]T     -> {"type":"object","additionalProperties":...}
//	struct           -> {"type":"object","properties":{...},"required":[...]}
//	指针              -> 解引用；该字段变为可选
//	json.RawMessage  -> 空 schema（任意类型）
//	interface{}      -> 空 schema（任意类型）
//
// 字段名取自 `json` tag（缺省时用字段名的小写形式）；
// 字段描述取自 `desc` tag。带 `omitempty` 或为指针的字段视为可选。
func schemaFromType(t reflect.Type) (json.RawMessage, error) {
	if t == nil {
		return nil, fmt.Errorf("nil type")
	}
	s, err := schemaFor(t)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// schemaFor 递归生成 schema 的内部表示。
func schemaFor(t reflect.Type) (map[string]any, error) {
	if t == rawMessageType {
		return map[string]any{}, nil
	}

	for t.Kind() == reflect.Pointer {
		t = t.Elem()
		if t == rawMessageType {
			return map[string]any{}, nil
		}
	}

	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}, nil
	case reflect.Bool:
		return map[string]any{"type": "boolean"}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}, nil
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}, nil
	case reflect.Slice, reflect.Array:
		item, err := schemaFor(t.Elem())
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "array", "items": item}, nil
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return nil, fmt.Errorf("unsupported map key type %s: only string keys are supported", t.Key())
		}
		val, err := schemaFor(t.Elem())
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "object", "additionalProperties": val}, nil
	case reflect.Struct:
		return structSchema(t)
	case reflect.Interface:
		return map[string]any{}, nil
	default:
		return nil, fmt.Errorf("unsupported type %s", t)
	}
}

// structSchema 为结构体生成 object schema。
func structSchema(t reflect.Type) (map[string]any, error) {
	props := make(map[string]any)
	var required []string

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}

		name, opts := parseJSONTag(f)
		if name == "-" {
			continue
		}

		s, err := schemaFor(f.Type)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", f.Name, err)
		}
		if desc := f.Tag.Get("desc"); desc != "" {
			s["description"] = desc
		}
		props[name] = s

		optional := f.Type.Kind() == reflect.Pointer || opts.contains("omitempty")
		if !optional {
			required = append(required, name)
		}
	}

	out := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		out["required"] = required
	}
	return out, nil
}

// parseJSONTag 解析字段的 json tag，返回字段名与选项。
func parseJSONTag(f reflect.StructField) (string, tagOptions) {
	tag := f.Tag.Get("json")
	if tag == "" {
		return strings.ToLower(f.Name), ""
	}
	parts := strings.Split(tag, ",")
	name := parts[0]
	if name == "" {
		name = strings.ToLower(f.Name)
	}
	if len(parts) == 1 {
		return name, ""
	}
	return name, tagOptions(strings.Join(parts[1:], ","))
}

// tagOptions 是 tag 中的逗号分隔选项。
type tagOptions string

// contains 报告选项是否存在。
func (o tagOptions) contains(opt string) bool {
	if o == "" {
		return false
	}
	for _, s := range strings.Split(string(o), ",") {
		if s == opt {
			return true
		}
	}
	return false
}
