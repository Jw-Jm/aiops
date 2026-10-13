package tools

import (
	"encoding/json"
	"ops-platform/api"
	"ops-platform/internal/investigation"
	"strings"
)

// MCP/provider input schemas must be self-contained. Resolve only this closed
// set of embedded contracts; neither MCP nor a model may fetch schema URLs.
func inlineAssistantSchema(schema map[string]any) (map[string]any, error) {
	var visit func(any, int) (any, error)
	visit = func(value any, depth int) (any, error) {
		if depth > 20 {
			return nil, investigation.ErrInvalid
		}
		switch v := value.(type) {
		case map[string]any:
			result := map[string]any{}
			if target, ok := v["$ref"].(string); ok {
				file := ""
				part := ""
				switch target {
				case "https://ops.local/schemas/query-intent/v1":
					file = "schemas/query-intent-v1.schema.json"
				case "https://ops.local/schemas/resource-selector/v1":
					file = "schemas/resource-selector-v1.schema.json"
				case "https://ops.local/schemas/common/v1#/$defs/canonicalId":
					file = "schemas/common.schema.json"
					part = "canonicalId"
				default:
					return nil, investigation.ErrInvalid
				}
				raw, err := api.Schemas.ReadFile(file)
				if err != nil {
					return nil, err
				}
				var document map[string]any
				if json.Unmarshal(raw, &document) != nil {
					return nil, investigation.ErrInvalid
				}
				if part != "" {
					document = document["$defs"].(map[string]any)[part].(map[string]any)
				}
				resolved, err := visit(document, depth+1)
				if err != nil {
					return nil, err
				}
				result = resolved.(map[string]any)
			}
			for key, child := range v {
				if key == "$ref" || key == "$id" || key == "$schema" || strings.HasPrefix(key, "x-") {
					continue
				}
				resolved, err := visit(child, depth+1)
				if err != nil {
					return nil, err
				}
				result[key] = resolved
			}
			return result, nil
		case []any:
			result := make([]any, len(v))
			for i, child := range v {
				resolved, err := visit(child, depth+1)
				if err != nil {
					return nil, err
				}
				result[i] = resolved
			}
			return result, nil
		default:
			return value, nil
		}
	}
	result, err := visit(schema, 0)
	if err != nil {
		return nil, err
	}
	return result.(map[string]any), nil
}
