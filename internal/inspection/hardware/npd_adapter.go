package hardware

import (
	"embed"
	"encoding/json"
	"regexp"
)

//go:embed kernel-monitor.json
var npdFiles embed.FS

func MatchKernelLog(line *string) ([]CheckResult, error) {
	if line == nil {
		return []CheckResult{{RuleID: "kernel-monitor", RuleVersion: "npd-v1.35.3", Status: "unknown", Degraded: true}}, nil
	}
	raw, err := npdFiles.ReadFile("kernel-monitor.json")
	if err != nil {
		return nil, err
	}
	var rules struct {
		Rules []struct{ Reason, Pattern, Type, Condition string }
	}
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil, err
	}
	out := []CheckResult{}
	for _, rule := range rules.Rules {
		pattern, err := regexp.Compile(rule.Pattern)
		if err != nil {
			return nil, err
		}
		if pattern.MatchString(*line) {
			out = append(out, CheckResult{RuleID: rule.Reason, RuleVersion: "npd-v1.35.3/kernel-monitor", Status: "degraded", Message: rule.Reason})
		}
	}
	// Absence of a matching log is not proof that the host is healthy.
	if len(out) == 0 {
		out = append(out, CheckResult{RuleID: "kernel-monitor", RuleVersion: "npd-v1.35.3", Status: "unknown", Degraded: true})
	}
	return out, nil
}
