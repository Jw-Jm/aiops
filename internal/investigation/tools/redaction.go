package tools

import (
	"encoding/json"
	"ops-platform/internal/investigation"
	"regexp"
	"strings"
)

var bearer = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]+`)
var secretAssignment = regexp.MustCompile(`(?i)(password|api[_-]?key|token|secret|authorization)\s*[:=]\s*[^\s,;]+`)
var urlCredential = regexp.MustCompile(`(?i)(https?://)[^\s/:@]+:[^\s/@]+@`)
var privateKey = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)

func clean(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			name := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(k))
			if strings.Contains(name, "password") || strings.Contains(name, "secret") || strings.Contains(name, "credential") || name == "authorization" || name == "token" || name == "apikey" || name == "accesstoken" || name == "refreshtoken" || name == "idtoken" || name == "sessiontoken" {
				x[k] = "[REDACTED]"
			} else {
				x[k] = clean(v)
			}
		}
		return x
	case []any:
		for i, v := range x {
			x[i] = clean(v)
		}
		return x
	case string:
		var encoded any
		if json.Unmarshal([]byte(x), &encoded) == nil {
			switch encoded.(type) {
			case map[string]any, []any:
				b, _ := json.Marshal(clean(encoded))
				return string(b)
			}
		}
		x = urlCredential.ReplaceAllString(x, "${1}[REDACTED]@")
		return secretAssignment.ReplaceAllString(bearer.ReplaceAllString(privateKey.ReplaceAllString(x, "[REDACTED]"), "Bearer [REDACTED]"), "[REDACTED]")
	default:
		return v
	}
}
func Sanitize(out Result, maxBytes int) (Result, error) {
	b, err := json.Marshal(out.Data)
	if err != nil {
		return Result{}, investigation.ErrInvalid
	}
	var v any
	if json.Unmarshal(b, &v) != nil {
		return Result{}, investigation.ErrInvalid
	}
	out.Data = clean(v)
	out.SchemaVersion = "tool-response/v2"
	if out.EvidenceRefs == nil {
		out.EvidenceRefs = []string{}
	}
	if out.DegradedSources == nil {
		out.DegradedSources = []string{}
	}
	b, _ = json.Marshal(out)
	if len(b) > maxBytes-4096 {
		out.Data = map[string]any{"truncated": true, "reason": "max_result_bytes"}
		out.Partial = true
		out.State = "partial"
	}
	return out, nil
}
