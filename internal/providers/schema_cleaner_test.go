package providers

import (
	"testing"
)

func TestCleanToolSchemas_Gemini(t *testing.T) {
	tools := []ToolDefinition{{
		Type: "function",
		Function: ToolFunctionSchema{
			Name:        "test",
			Description: "desc",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":    "string",
						"default": "world",
					},
				},
				"$defs":                map[string]any{"Foo": "bar"},
				"additionalProperties": false,
				"examples":             []any{"a"},
			},
		},
	}}

	cleaned := CleanToolSchemas("gemini", tools)
	if len(cleaned) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(cleaned))
	}

	params := cleaned[0].Function.Parameters
	for _, key := range []string{"$defs", "additionalProperties", "examples"} {
		if _, ok := params[key]; ok {
			t.Errorf("expected key %q to be removed", key)
		}
	}

	// "type" should remain
	if _, ok := params["type"]; !ok {
		t.Error("expected 'type' to remain")
	}

	// Nested "default" should be removed
	props := params["properties"].(map[string]any)
	nameSchema := props["name"].(map[string]any)
	if _, ok := nameSchema["default"]; ok {
		t.Error("expected nested 'default' to be removed for gemini")
	}
	if _, ok := nameSchema["type"]; !ok {
		t.Error("expected nested 'type' to remain")
	}
}

func TestCleanToolSchemas_Anthropic(t *testing.T) {
	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type": "string",
				"$ref": "#/$defs/URL",
			},
		},
		"$defs":                map[string]any{"URL": "..."},
		"additionalProperties": false,
		"default":              "x",
	}

	cleaned := CleanSchemaForProvider("anthropic", params)

	// $ref and $defs removed
	if _, ok := cleaned["$defs"]; ok {
		t.Error("expected $defs removed for anthropic")
	}
	props := cleaned["properties"].(map[string]any)
	urlSchema := props["url"].(map[string]any)
	if _, ok := urlSchema["$ref"]; ok {
		t.Error("expected nested $ref removed for anthropic")
	}

	// additionalProperties and default should remain for anthropic
	if _, ok := cleaned["additionalProperties"]; !ok {
		t.Error("expected additionalProperties to remain for anthropic")
	}
	if _, ok := cleaned["default"]; !ok {
		t.Error("expected default to remain for anthropic")
	}
}

func TestCleanToolSchemas_Unknown(t *testing.T) {
	tools := []ToolDefinition{{
		Type: "function",
		Function: ToolFunctionSchema{
			Name: "test",
			Parameters: map[string]any{
				"$ref":    "something",
				"default": "val",
			},
		},
	}}

	cleaned := CleanToolSchemas("openrouter", tools)
	// Should return original unchanged
	if _, ok := cleaned[0].Function.Parameters["$ref"]; !ok {
		t.Error("expected $ref to remain for unknown provider")
	}
}

func TestCleanToolSchemas_Empty(t *testing.T) {
	cleaned := CleanToolSchemas("gemini", nil)
	if cleaned != nil {
		t.Error("expected nil for nil tools")
	}
}

func TestCleanSchema_NilParams(t *testing.T) {
	result := CleanSchemaForProvider("gemini", nil)
	if result != nil {
		t.Error("expected nil for nil params")
	}
}

func TestCleanSchema_NestedArray(t *testing.T) {
	params := map[string]any{
		"anyOf": []any{
			map[string]any{
				"type":    "string",
				"default": "x",
			},
			map[string]any{
				"type":    "number",
				"$ref":    "#/defs/Num",
				"default": 42,
			},
		},
	}

	cleaned := CleanSchemaForProvider("gemini", params)
	anyOf := cleaned["anyOf"].([]any)
	if len(anyOf) != 2 {
		t.Fatalf("expected 2 items, got %d", len(anyOf))
	}

	first := anyOf[0].(map[string]any)
	if _, ok := first["default"]; ok {
		t.Error("expected 'default' removed in array item")
	}
	if _, ok := first["type"]; !ok {
		t.Error("expected 'type' to remain in array item")
	}

	second := anyOf[1].(map[string]any)
	if _, ok := second["$ref"]; ok {
		t.Error("expected '$ref' removed in array item")
	}
	if _, ok := second["default"]; ok {
		t.Error("expected 'default' removed in array item")
	}
}

func TestCleanSchema_DeepNesting(t *testing.T) {
	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"config": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"nested": map[string]any{
						"type":    "string",
						"default": "deep",
						"$ref":    "#/deep",
					},
				},
			},
		},
	}

	cleaned := CleanSchemaForProvider("gemini", params)
	props := cleaned["properties"].(map[string]any)
	config := props["config"].(map[string]any)
	innerProps := config["properties"].(map[string]any)
	nested := innerProps["nested"].(map[string]any)

	if _, ok := nested["default"]; ok {
		t.Error("expected deeply nested 'default' removed")
	}
	if _, ok := nested["$ref"]; ok {
		t.Error("expected deeply nested '$ref' removed")
	}
	if _, ok := nested["type"]; !ok {
		t.Error("expected 'type' to remain at deep level")
	}
}
