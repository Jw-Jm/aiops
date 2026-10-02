package kubernetes

import "strings"

func safeString(v any) (string, bool) { s, ok := v.(string); return s, ok && len(s) <= 1024 }
func safeReference(v any) map[string]any {
	return selectStrings(v, "apiVersion", "kind", "namespace", "name", "uid", "resourceVersion", "fieldPath")
}
func selectStrings(v any, keys ...string) map[string]any {
	out := map[string]any{}
	m, _ := v.(map[string]any)
	for _, k := range keys {
		if s, ok := safeString(m[k]); ok {
			out[k] = s
		}
	}
	return out
}
func safeLabels(v any) map[string]any {
	out := map[string]any{}
	m, _ := v.(map[string]any)
	for k, value := range m {
		lower := strings.ToLower(k)
		if strings.Contains(lower, "password") || strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "credential") {
			continue
		}
		if s, ok := safeString(value); ok && len(s) <= 256 && len(k) <= 253 {
			out[k] = s
		}
	}
	return out
}
func projectNested(path []string, v any) any {
	switch strings.Join(path, ".") {
	case "spec.selector":
		m, _ := v.(map[string]any)
		if _, exists := m["matchLabels"]; !exists {
			if _, exists := m["matchExpressions"]; !exists {
				return safeLabels(v)
			}
		}
		out := map[string]any{}
		if labels, ok := m["matchLabels"]; ok {
			out["matchLabels"] = safeLabels(labels)
		}
		if expressions, ok := m["matchExpressions"].([]any); ok {
			items := []any{}
			for _, raw := range expressions {
				item := selectStrings(raw, "key", "operator")
				e, _ := raw.(map[string]any)
				values := []any{}
				for _, v := range sliceValues(e["values"]) {
					if s, ok := safeString(v); ok && len(s) <= 256 {
						values = append(values, s)
					}
				}
				item["values"] = values
				items = append(items, item)
			}
			out["matchExpressions"] = items
		}
		return out
	case "spec.claimRef", "spec.scaleTargetRef", "involvedObject", "regarding", "roleRef":
		return safeReference(v)
	case "subjects":
		out := []any{}
		for _, m := range sliceValues(v) {
			out = append(out, selectStrings(m, "apiGroup", "kind", "name", "namespace"))
		}
		return out
	case "status.addresses":
		out := []any{}
		for _, m := range sliceValues(v) {
			out = append(out, selectStrings(m, "type", "address"))
		}
		return out
	case "endpoints":
		out := []any{}
		for _, raw := range sliceValues(v) {
			m, _ := raw.(map[string]any)
			item := selectStrings(raw, "hostname", "nodeName", "zone")
			addresses := []any{}
			for _, v := range sliceValues(m["addresses"]) {
				if s, ok := safeString(v); ok {
					addresses = append(addresses, s)
				}
			}
			item["addresses"] = addresses
			if ref, ok := m["targetRef"]; ok {
				item["targetRef"] = safeReference(ref)
			}
			if conditions, ok := m["conditions"].(map[string]any); ok {
				safe := map[string]any{}
				for _, k := range []string{"ready", "serving", "terminating"} {
					if b, ok := conditions[k].(bool); ok {
						safe[k] = b
					}
				}
				item["conditions"] = safe
			}
			out = append(out, item)
		}
		return out
	default:
		if s, ok := safeString(v); ok {
			return s
		}
		if n, ok := v.(int64); ok {
			return n
		}
		if n, ok := v.(float64); ok {
			return n
		}
		return nil
	}
}
func sliceValues(v any) []any { s, _ := v.([]any); return s }
