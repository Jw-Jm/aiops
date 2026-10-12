package investigation

import (
	"encoding/json"
	"strings"
)

// SafeInputSummary projects public scope and budget fields, never raw queries or model prompts.
func SafeInputSummary(name string, arguments json.RawMessage) json.RawMessage {
	if name == "model" {
		return json.RawMessage(`{"context":"已授权故障上下文与持久工具结果","rawPromptOmitted":true}`)
	}
	switch name {
	case "get_incident_context", "get_resource_context", "get_resource_health", "get_dependencies", "get_impact_scope", "get_findings", "get_ranked_evidence", "get_evidence_conflicts", "get_recent_changes", "get_root_cause_candidates", "query_metrics", "query_logs", "query_deepflow", "query_kubernetes", "query_hardware":
	default:
		return json.RawMessage(`{}`)
	}
	var args map[string]any
	if len(arguments) > 65536 || json.Unmarshal(arguments, &args) != nil {
		return json.RawMessage(`{}`)
	}
	summary := map[string]any{}
	for _, key := range []string{"canonicalId", "resourceCanonicalId", "incidentId", "namespace", "limit", "depth", "maxNodes", "maxEdges", "maxPoints", "stepSeconds"} {
		switch v := args[key].(type) {
		case string:
			if len(v) <= 1024 {
				summary[key] = v
			}
		case float64:
			summary[key] = v
		case bool:
			summary[key] = v
		}
	}
	if tr, ok := args["timeRange"].(map[string]any); ok {
		for _, key := range []string{"start", "end", "from", "to"} {
			if v, ok := tr[key].(string); ok && len(v) <= 128 {
				summary["timeRange"+strings.ToUpper(key[:1])+key[1:]] = v
			}
		}
	}
	if strings.HasPrefix(name, "query_") {
		summary["rawQueryOmitted"] = true
	}
	out, err := json.Marshal(summary)
	if err != nil || len(out) > 4096 {
		return json.RawMessage(`{}`)
	}
	return out
}
