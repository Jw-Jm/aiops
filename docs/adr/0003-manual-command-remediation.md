# ADR-0003: Operator-Entered Command Remediation

Status: Accepted

## Context and rationale

The product may suggest concrete remediation commands, but diagnosis or model output cannot authorize an operation. The execution audit must distinguish what the platform suggested from what an operator chose to run, including when the two commands differ.

## Decision

The platform presents a suggested real command and its initial risk. An authorized `operator` enters the actual Bash command in a blank input field; it may differ from the suggestion. The platform re-evaluates the actual command, displays its risk, and binds the operator's confirmation to that command digest. Cluster-level and root-level execution also requires Keycloak step-up with one-hour absolute and idle expiry. Authorization, OPA policy, and ExecutionProfile checks then gate dispatch to an isolated Runner using short-lived credentials. Persist a distinct `CommandExecution` before dispatch, audit the result, and run Post-check. Keep ActionPlan recommendations separate from execution records.

## Alternatives considered

- Execute the agent's suggested command automatically: rejected because an agent or background task must never authorize execution.
- Require a second-person approval: rejected by the 1.0 remediation contract, which uses the same operator's risk confirmation and required step-up.
- Define a command DSL: rejected; 1.0 accepts operator-entered real Bash text for supported Linux, Kylin, and Kubernetes targets.

## Consequences

The operator makes the final command choice and may need to confirm again after editing it. The system must separately retain the suggested command, actual command, risk decision, confirmation, step-up evidence, target, output, exit state, and Post-check. An uncertain dispatch becomes `execution_unknown`; it must be reconciled before any new request, because arbitrary shell commands are not presumed idempotent.

## Implementation boundaries

- Agents and background jobs cannot call command execution or hold execution credentials.
- Reclassify and bind risk confirmation to the actual command; command differences alone are not a rejection reason.
- Do not add a second approver, a command DSL, automatic execution, or automatic retries for uncertain commands.
- Do not merge actual execution data into the ActionPlan recommendation.

## Rollback conditions

Reconsider this flow only if an approved product-scope or security requirement changes the operator-confirmation model. A new accepted ADR and updated threat, Contract, and audit review are required before changing the execution gate; no implementation may bypass confirmation, step-up, policy, durable execution records, or reconciliation.
