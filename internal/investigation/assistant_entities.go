package investigation

import "encoding/json"

// Only platform-owned identity fields in these tool outputs establish an entity
// reference. Nested annotations, symptoms and arbitrary evidence payloads cannot.
func committedAssistantEntities(tool string, data json.RawMessage) []string {
	type identity struct {
		CanonicalID string `json:"canonicalId"`
	}
	type resourceContext struct {
		Resources []identity `json:"resources"`
	}
	var v struct {
		Nodes      []identity      `json:"nodes"`
		Candidates []identity      `json:"candidates"`
		Context    resourceContext `json:"context"`
		Binding    struct {
			ResourceContext resourceContext `json:"resourceContext"`
		} `json:"binding"`
	}
	if json.Unmarshal(data, &v) != nil {
		return nil
	}
	var identities []identity
	switch tool {
	case "get_assistant_context":
		identities = v.Binding.ResourceContext.Resources
	case "get_resource_investigation_context":
		identities = v.Context.Resources
	case "resolve_resources":
		identities = v.Candidates
	case "get_resource_context", "get_resource_health", "query_kubernetes", "get_dependencies", "get_impact_scope":
		identities = v.Nodes
	default:
		return nil
	}
	out := []string{}
	seen := map[string]bool{}
	for _, entity := range identities {
		if entity.CanonicalID != "" && !seen[entity.CanonicalID] {
			out = append(out, entity.CanonicalID)
			seen[entity.CanonicalID] = true
		}
	}
	return out
}
