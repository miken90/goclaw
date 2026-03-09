package providers

import "slices"

import "strings"

// Unsupported schema keys by provider.
// Gemini rejects: $ref, $defs, additionalProperties, examples, default.
// Anthropic doesn't use: $ref, $defs.
var (
	geminiUnsupportedKeys    = []string{"$ref", "$defs", "additionalProperties", "examples", "default"}
	anthropicUnsupportedKeys = []string{"$ref", "$defs"}
)

// CleanToolSchemas returns a copy of tools with provider-incompatible
// JSON Schema fields removed from each tool's parameters.
// Returns the original slice unchanged for providers that need no cleaning.
func CleanToolSchemas(providerName string, tools []ToolDefinition) []ToolDefinition {
	removeKeys := unsupportedKeysForProvider(providerName)
	if removeKeys == nil || len(tools) == 0 {
		return tools
	}

	cleaned := make([]ToolDefinition, len(tools))
	for i, t := range tools {
		cleaned[i] = ToolDefinition{
			Type: t.Type,
			Function: ToolFunctionSchema{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  cleanSchema(t.Function.Parameters, removeKeys),
			},
		}
	}
	return cleaned
}

// CleanSchemaForProvider cleans a single parameters map for a provider.
func CleanSchemaForProvider(providerName string, params map[string]any) map[string]any {
	removeKeys := unsupportedKeysForProvider(providerName)
	if removeKeys == nil {
		return params
	}
	return cleanSchema(params, removeKeys)
}

func unsupportedKeysForProvider(name string) []string {
	switch {
	case name == "gemini" || strings.HasPrefix(name, "gemini-"):
		return geminiUnsupportedKeys
	case name == "anthropic":
		return anthropicUnsupportedKeys
	default:
		return nil
	}
}

// cleanSchema recursively removes unsupported keys from a JSON Schema map.
func cleanSchema(schema map[string]any, removeKeys []string) map[string]any {
	if schema == nil {
		return nil
	}

	result := make(map[string]any, len(schema))
	for k, v := range schema {
		if shouldRemoveKey(k, removeKeys) {
			continue
		}

		switch val := v.(type) {
		case map[string]any:
			result[k] = cleanSchema(val, removeKeys)
		case []any:
			result[k] = cleanSchemaSlice(val, removeKeys)
		default:
			result[k] = v
		}
	}
	return result
}

// cleanSchemaSlice recurses into arrays (e.g. "anyOf", "oneOf", "allOf").
func cleanSchemaSlice(items []any, removeKeys []string) []any {
	result := make([]any, len(items))
	for i, item := range items {
		if m, ok := item.(map[string]any); ok {
			result[i] = cleanSchema(m, removeKeys)
		} else {
			result[i] = item
		}
	}
	return result
}

func shouldRemoveKey(key string, removeKeys []string) bool {
	return slices.Contains(removeKeys, key)
}
