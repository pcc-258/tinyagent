package tinyagent

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

type weatherParams struct {
	City string `json:"city" desc:"城市名"`
	Unit string `json:"unit,omitempty" desc:"温度单位"`
	Days int    `json:"days" desc:"预报天数"`
}

func mustNewTool[T any](
	t *testing.T,
	name, desc string,
	fn func(context.Context, T) (ToolResult, error),
) *FuncTool[T] {
	t.Helper()
	tool, err := NewFuncTool(name, desc, fn)
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

func noopFn(ctx context.Context, p weatherParams) (ToolResult, error) {
	return ToolResult{Content: "ok"}, nil
}

// TestNewFuncTool_Schema 验证从 Go 结构体生成的 schema 正确。
func TestNewFuncTool_Schema(t *testing.T) {
	tool := mustNewTool(t, "get_weather", "查询天气", noopFn)

	var schema map[string]any
	if err := json.Unmarshal(tool.Info().Parameters, &schema); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("type = %v, want object", schema["type"])
	}

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties missing: %v", schema)
	}
	for _, want := range []string{"city", "unit", "days"} {
		if _, ok := props[want]; !ok {
			t.Errorf("missing property %q", want)
		}
	}

	city := props["city"].(map[string]any)
	if city["type"] != "string" {
		t.Errorf("city.type = %v, want string", city["type"])
	}
	if city["description"] != "城市名" {
		t.Errorf("city.description = %v, want 城市名", city["description"])
	}

	days := props["days"].(map[string]any)
	if days["type"] != "integer" {
		t.Errorf("days.type = %v, want integer", days["type"])
	}

	req, _ := schema["required"].([]any)
	required := map[string]bool{}
	for _, r := range req {
		required[r.(string)] = true
	}
	if !required["city"] || !required["days"] {
		t.Errorf("required = %v, want city and days", req)
	}
	if required["unit"] {
		t.Error("unit has omitempty and should be optional")
	}
}

// TestFuncTool_Run 验证工具执行。
func TestFuncTool_Run(t *testing.T) {
	tool := mustNewTool(t, "echo", "回显",
		func(ctx context.Context, p weatherParams) (ToolResult, error) {
			return ToolResult{Content: "city=" + p.City}, nil
		})

	res, err := tool.Run(context.Background(), json.RawMessage(`{"city":"北京","days":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "city=北京" {
		t.Errorf("Content = %q, want city=北京", res.Content)
	}
	if res.IsError() {
		t.Error("should not be an error result")
	}
}

// TestFuncTool_BadArguments 验证参数解析失败是「业务软错误」而非框架错误。
func TestFuncTool_BadArguments(t *testing.T) {
	tool := mustNewTool(t, "echo", "回显", noopFn)

	res, err := tool.Run(context.Background(), json.RawMessage(`{"city":123}`))
	if err != nil {
		t.Fatalf("bad arguments should be a soft error, not a framework error: %v", err)
	}
	if !res.IsError() {
		t.Error("expected soft error result")
	}
}

// TestNewFuncTool_Errors 验证构造期的参数校验。
func TestNewFuncTool_Errors(t *testing.T) {
	if _, err := NewFuncTool[weatherParams]("", "d", noopFn); err == nil {
		t.Error("empty name should error")
	}
	if _, err := NewFuncTool[weatherParams]("x", "d", nil); err == nil {
		t.Error("nil fn should error")
	}
}

// TestToolRegistry 验证注册表的注册、查找、去重与稳定顺序。
func TestToolRegistry(t *testing.T) {
	reg := NewToolRegistry()
	a := mustNewTool(t, "a", "tool a", noopFn)
	b := mustNewTool(t, "b", "tool b", noopFn)

	if err := reg.Register(a); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(b); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(a); err == nil {
		t.Error("duplicate registration should error")
	}
	if err := reg.Register(nil); err == nil {
		t.Error("nil tool should error")
	}
	if reg.Len() != 2 {
		t.Errorf("Len = %d, want 2", reg.Len())
	}
	if _, ok := reg.Lookup("a"); !ok {
		t.Error("Lookup(a) failed")
	}
	if _, ok := reg.Lookup("zzz"); ok {
		t.Error("Lookup(zzz) should fail")
	}

	list := reg.List()
	if len(list) != 2 || list[0].Info().Name != "a" || list[1].Info().Name != "b" {
		t.Errorf("List order not stable: %v", list)
	}
	if len(reg.Infos()) != 2 {
		t.Errorf("Infos length = %d, want 2", len(reg.Infos()))
	}
}

// TestSchemaFromType_Nested 覆盖 slice / map / 指针 / 嵌套结构体 / json.RawMessage。
func TestSchemaFromType_Nested(t *testing.T) {
	type inner struct {
		ID int `json:"id" desc:"标识"`
	}
	type outer struct {
		Tags   []string          `json:"tags" desc:"标签"`
		Attrs  map[string]string `json:"attrs" desc:"属性"`
		Ptr    *int              `json:"ptr,omitempty" desc:"可空整数"`
		Nested inner             `json:"nested" desc:"嵌套对象"`
		Raw    json.RawMessage   `json:"raw" desc:"任意"`
	}

	raw, err := schemaFromType(reflect.TypeOf(outer{}))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	props := schema["properties"].(map[string]any)

	if props["tags"].(map[string]any)["type"] != "array" {
		t.Error("tags should be array")
	}
	if props["attrs"].(map[string]any)["type"] != "object" {
		t.Error("attrs should be object")
	}
	if props["ptr"].(map[string]any)["type"] != "integer" {
		t.Errorf("ptr should resolve to integer, got %v", props["ptr"])
	}
	nested := props["nested"].(map[string]any)
	if nested["type"] != "object" {
		t.Error("nested should be object")
	}
	if _, ok := nested["properties"].(map[string]any)["id"]; !ok {
		t.Error("nested.id missing")
	}
	if _, hasType := props["raw"].(map[string]any)["type"]; hasType {
		t.Errorf("json.RawMessage should map to empty schema, got %v", props["raw"])
	}
}
