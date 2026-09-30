package ops.policy

default action_decision := {
	"allow": false,
	"risk": "high",
	"reasons": ["action_default_deny"],
	"requiresStepUp": false,
}

action_decision := {
	"allow": false,
	"risk": input.risk,
	"reasons": ["step_up_required"],
	"requiresStepUp": true,
} if {
	input.requestType == "action"
	input.tenantMatched
	input.scopeAllowed
	input.actionAllowed
	input.requiresStepUp
	not input.stepUpVerified
}

action_decision := {
	"allow": true,
	"risk": input.risk,
	"reasons": [],
	"requiresStepUp": input.requiresStepUp,
} if {
	input.requestType == "action"
	input.tenantMatched
	input.scopeAllowed
	input.actionAllowed
	input.commandDigestMatched
	input.riskAcknowledged
	stepUpSatisfied
}

stepUpSatisfied if {
	not input.requiresStepUp
}

stepUpSatisfied if {
	input.requiresStepUp
	input.stepUpVerified
}
