package openai

import (
	"maps"
	"slices"

	"github.com/openai/openai-go/v3/shared"

	"github.com/docker/docker-agent/pkg/tools"
)

// ConvertParametersToSchema converts parameters to OpenAI Schema format and
// reports whether the resulting schema is compatible with OpenAI strict mode.
//
// The same normalization pipeline runs in both cases — strict-incompatible
// schemas (e.g. Notion MCP tools that declare schema-form additionalProperties)
// still need fully-populated `required` arrays for the Chat Completions API,
// which has no per-tool strict flag. The strict flag is only consumed by the
// Responses API caller.
func ConvertParametersToSchema(params any) (shared.FunctionParameters, bool, error) {
	p, err := tools.SchemaToMap(params)
	if err != nil {
		return nil, false, err
	}

	strict := isStrictCompatible(p)
	return fixSchemaArrayItems(removeFormatFields(ensureTypeFields(makeAllRequired(p)))), strict, nil
}

// isStrictCompatible reports whether the schema can use OpenAI strict mode.
// Strict mode requires every object node to have additionalProperties: false.
// Schema-form additionalProperties (a map) and additionalProperties: true are
// both incompatible.
//
// The decision is per-tool and all-or-nothing: a single non-compliant node
// anywhere in the schema disables strict mode for the whole tool. The walk
// stops at the first incompatible node.
func isStrictCompatible(schema map[string]any) bool {
	return !hasIncompatibleNode(schema)
}

func hasIncompatibleNode(node map[string]any) bool {
	if v, ok := node["additionalProperties"]; ok {
		switch t := v.(type) {
		case map[string]any:
			return true
		case bool:
			if t {
				return true
			}
		}
	}

	if properties, ok := node["properties"].(map[string]any); ok {
		for _, v := range properties {
			if sub, ok := v.(map[string]any); ok && hasIncompatibleNode(sub) {
				return true
			}
		}
	}

	for _, keyword := range []string{"anyOf", "oneOf", "allOf"} {
		if variants, ok := node[keyword].([]any); ok {
			for _, v := range variants {
				if sub, ok := v.(map[string]any); ok && hasIncompatibleNode(sub) {
					return true
				}
			}
		}
	}

	if items, ok := node["items"].(map[string]any); ok && hasIncompatibleNode(items) {
		return true
	}

	if additionalProps, ok := node["additionalProperties"].(map[string]any); ok && hasIncompatibleNode(additionalProps) {
		return true
	}

	return false
}

// walkSchema calls fn on the given schema node, then recursively walks into
// properties, anyOf/oneOf/allOf variants, array items, and additionalProperties.
func walkSchema(schema map[string]any, fn func(map[string]any)) {
	fn(schema)

	if properties, ok := schema["properties"].(map[string]any); ok {
		for _, v := range properties {
			if sub, ok := v.(map[string]any); ok {
				walkSchema(sub, fn)
			}
		}
	}

	for _, keyword := range []string{"anyOf", "oneOf", "allOf"} {
		if variants, ok := schema[keyword].([]any); ok {
			for _, v := range variants {
				if sub, ok := v.(map[string]any); ok {
					walkSchema(sub, fn)
				}
			}
		}
	}

	if items, ok := schema["items"].(map[string]any); ok {
		walkSchema(items, fn)
	}

	// additionalProperties can be a boolean or an object schema
	if additionalProps, ok := schema["additionalProperties"].(map[string]any); ok {
		walkSchema(additionalProps, fn)
	}
}

// makeAllRequired makes every object property `required` (newly-required ones
// are made nullable) and ensures every object node has `additionalProperties`
// set. It runs on every schema regardless of strict-mode compatibility, so
// schema-form additionalProperties (e.g. Notion's dictionary value shape) is
// preserved — only missing/true/nil values are forced to `false`.
func makeAllRequired(schema shared.FunctionParameters) shared.FunctionParameters {
	if schema == nil {
		schema = map[string]any{"type": "object", "properties": map[string]any{}}
	}

	walkSchema(schema, func(node map[string]any) {
		isObject := false
		if typeVal, ok := node["type"]; ok {
			switch t := typeVal.(type) {
			case string:
				isObject = t == "object"
			case []any:
				for _, v := range t {
					if s, ok := v.(string); ok && s == "object" {
						isObject = true
						break
					}
				}
			case []string:
				isObject = slices.Contains(t, "object")
			}
		}

		// Only force additionalProperties: false when it isn't already a
		// schema. Schema-form additionalProperties carries information the
		// model needs (Notion-style dictionaries) and would be lost otherwise.
		if isObject {
			if addProps, exists := node["additionalProperties"]; !exists || addProps == nil || addProps == true {
				node["additionalProperties"] = false
			}
		}

		properties, ok := node["properties"].(map[string]any)
		if !ok {
			return
		}

		originallyRequired := map[string]bool{}
		if required, ok := node["required"].([]any); ok {
			for _, name := range required {
				originallyRequired[name.(string)] = true
			}
		}

		newRequired := []any{}
		for _, propName := range slices.Sorted(maps.Keys(properties)) {
			newRequired = append(newRequired, propName)
			if !originallyRequired[propName] {
				if propMap, ok := properties[propName].(map[string]any); ok {
					if t, ok := propMap["type"].(string); ok {
						propMap["type"] = []string{t, "null"}
					}
				}
			}
		}

		node["required"] = newRequired
	})

	return schema
}

// ensureTypeFields ensures every schema node that is a map has a "type" key.
// OpenAI Responses API requires all schema nodes to have an explicit type.
// Nodes with "properties" default to "object"; other nodes default to "object" as well.
func ensureTypeFields(schema shared.FunctionParameters) shared.FunctionParameters {
	if schema == nil {
		return nil
	}

	walkSchema(schema, func(node map[string]any) {
		if _, hasType := node["type"]; !hasType {
			node["type"] = "object"
		}
	})

	return schema
}

// removeFormatFields removes the "format" field from all nodes in the schema.
// OpenAI does not support the JSON Schema "format" keyword (e.g. "uri", "email", "date").
func removeFormatFields(schema shared.FunctionParameters) shared.FunctionParameters {
	if schema == nil {
		return nil
	}

	walkSchema(schema, func(node map[string]any) {
		delete(node, "format")
	})

	return schema
}

// In Docker Desktop 4.52, the MCP Gateway produces an invalid tools shema for `mcp-config-set`.
func fixSchemaArrayItems(schema shared.FunctionParameters) shared.FunctionParameters {
	propertiesValue, ok := schema["properties"]
	if !ok {
		return schema
	}

	properties, ok := propertiesValue.(map[string]any)
	if !ok {
		return schema
	}

	for _, propValue := range properties {
		prop, ok := propValue.(map[string]any)
		if !ok {
			continue
		}

		checkForMissingItems := false
		switch t := prop["type"].(type) {
		case string:
			checkForMissingItems = t == "array"
		case []string:
			checkForMissingItems = slices.Contains(t, "array")
		}
		if !checkForMissingItems {
			continue
		}

		if _, ok := prop["items"]; !ok {
			prop["items"] = map[string]any{"type": "object"}
		}
	}

	return schema
}
