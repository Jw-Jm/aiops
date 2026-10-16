package investigation

import "slices"

// Source receipts determine collection status independently of model prose.
func assistantReadStatus(a *AssistantAnswer, tool, state string, partial bool, code, executionMode string) {
	if executionMode == "evidence_only" {
		return
	}
	warning := ""
	if state == "failed" {
		if code == "" {
			code = "READ_NOT_COMPLETED"
		}
		warning = "source_read_failed:" + tool + ":" + code
	} else if partial {
		warning = "source_read_partial:" + tool
	}
	if warning != "" {
		a.Partial = true
		if !slices.Contains(a.Warnings, warning) {
			a.Warnings = append(a.Warnings, warning)
		}
	}
}
