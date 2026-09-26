# ADR-0002: Two-Role Authorization Model

Status: Accepted

## Context and rationale

The product needs both scoped operation and platform administration, while Kubernetes, KubeVirt, hardware, and audit responsibilities are organizational assignments rather than separate authorization identities. A small role set avoids implicit privilege growth and keeps authorization tied to tenant and resource scope.

## Decision

Define exactly two platform roles: `operator` and `platform_admin`. They do not inherit from each other. `operator` acts only within explicitly granted tenant, cluster, namespace, and resource scopes. `platform_admin` manages platform configuration and authorized audit export, but does not gain command-operation rights automatically. A person who must operate resources must also receive an explicit `operator` grant.

## Alternatives considered

- Add separate approver, auditor, Kubernetes, KubeVirt, and hardware roles: rejected because the product contract models these as scoped permissions or business responsibilities.
- Make `platform_admin` inherit `operator`: rejected because administration must not silently grant operational execution authority.

## Consequences

Authorization checks must combine the role with explicit scope and permission grants. Administrative and operational duties can be assigned separately, and audit review remains available to authorized administrators without an independent auditor role.

## Implementation boundaries

- Do not add role inheritance or a third business role for audit, approval, or technical specialty.
- Do not treat a client-supplied tenant, cluster, or namespace as an authorization grant.
- Do not allow a platform administrator to execute a command without an explicit `operator` authorization.

## Rollback conditions

Revisit this model only if a documented product or compliance requirement cannot be met through scoped grants on these two roles. Any role-model change requires an accepted ADR, an authorization migration plan, and updated Contract and least-privilege tests before implementation.
