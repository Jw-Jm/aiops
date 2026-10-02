package hardware

import (
	"math"
	"ops-platform/internal/upstream/corootcheck"
)

type CheckResult struct {
	RuleID      string `json:"ruleId"`
	RuleVersion string `json:"ruleVersion"`
	Status      string `json:"status"`
	Message     string `json:"message"`
	Degraded    bool   `json:"degraded"`
}

func CheckThreshold(rule string, value *float32, threshold float32) CheckResult {
	out := CheckResult{RuleID: rule, RuleVersion: "coroot-v1.26.8/Check.Calc/value-v1", Status: "unknown", Degraded: true}
	if value == nil || math.IsNaN(float64(*value)) || math.IsInf(float64(*value), 0) || math.IsNaN(float64(threshold)) || math.IsInf(float64(threshold), 0) {
		return out
	}
	check := corootcheck.EvaluateValue(*value, threshold, "value {{.Value}} exceeds threshold {{.Threshold}}")
	out.Degraded = check.Status == corootcheck.UNKNOWN
	out.Message = check.Message
	switch check.Status {
	case corootcheck.OK:
		out.Status = "normal"
	case corootcheck.WARNING:
		out.Status = "degraded"
	}
	return out
}
