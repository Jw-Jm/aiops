package hardware

import (
	"embed"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
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

// MatchKernelRecord preserves native multiline record boundaries while replaying
// the unchanged NPD single-line rules. It never joins unrelated log rows.
func MatchKernelRecord(message string) ([]CheckResult, error) {
	if len(message) > 4096 || strings.Count(message, "\n") >= 64 {
		return nil, errors.New("kernel record budget exhausted")
	}
	out := []CheckResult{}
	seen := map[string]bool{}
	for _, line := range strings.Split(message, "\n") {
		matches, err := MatchKernelLog(&line)
		if err != nil {
			return nil, err
		}
		for _, match := range matches {
			if !match.Degraded && !seen[match.RuleID] {
				seen[match.RuleID] = true
				out = append(out, match)
			}
		}
	}
	return out, nil
}
