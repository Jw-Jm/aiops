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

# Audit assistant permission is distinct from operational tool access. Existing
# admin requests without a v3 binding remain denied.
runtime_principal if {
 input.principalType == "platform_admin"
 input.taskKind == "data_query"
 input.authorizationDomain == "audit"
 input.toolName in {"get_assistant_context", "describe_analysis_dataset", "execute_analysis_query"}
 audit_dataset
}

audit_dataset if { input.datasetId == "" }
audit_dataset if { input.datasetId == "audit.events" }
