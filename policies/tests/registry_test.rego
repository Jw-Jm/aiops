package ops.policy

test_tool_default_denies if {
	decision := data.ops.policy.tool_decision with input as {"requestType": "tool"}
	decision.allow == false
}

test_tool_allows_scoped_read_only_operator if {
	decision := data.ops.policy.tool_decision with input as {
		"requestType": "tool",
		"tenantMatched": true,
		"scopeAllowed": true,
		"toolAllowed": true,
		"toolReadOnly": true,
		"principalType": "operator",
		"risk": "low",
	}
	decision.allow == true
}

test_action_requires_step_up if {
	decision := data.ops.policy.action_decision with input as {
		"requestType": "action",
		"tenantMatched": true,
		"scopeAllowed": true,
		"actionAllowed": true,
		"requiresStepUp": true,
		"stepUpVerified": false,
		"risk": "low",
	}
	decision.allow == false
	decision.requiresStepUp == true
}
