package ops.policy

default tool_decision := {
	"allow": false,
	"risk": "high",
	"reasons": ["tool_default_deny"],
	"requiresStepUp": false,
}

tool_decision := {
	"allow": true,
	"risk": input.risk,
	"reasons": [],
	"requiresStepUp": false,
} if {
	input.requestType == "tool"
	input.tenantMatched
	input.scopeAllowed
	input.toolAllowed
	input.toolReadOnly
	runtime_principal
}

runtime_principal if {
	input.principalType == "operator"
}

runtime_principal if {
	input.principalType == "agent"
}
