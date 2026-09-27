# ADR-0006: DeepFlow as a Profile-Selected Add-on

Status: Accepted

## Context and rationale

DeepFlow can provide dynamic network evidence, but it is not currently deployed and its eBPF behavior must be verified in the development environment. The platform must preserve an honest capability boundary without turning DeepFlow's internal storage or full application stack into platform dependencies.

## Decision

Treat DeepFlow as an independent add-on selected by Deployment Profile as `external`, `bundled`, or `disabled`. The platform accesses it only through `DeepFlowEvidenceAdapter` and Server/Querier APIs; it does not use ClickHouse business-query credentials. The DeepFlow plane has one platform-level Server/storage plane and one Agent DaemonSet per managed Kubernetes cluster. Use ClusterIP services. Do not install agents in VM guests or arbitrary SSH hosts and do not create a second central plane automatically. DeepFlow v7.2.0 remains a candidate until the OrbStack PoC validates registration, flow/eBPF, dependency, and retransmission queries. If live Agent capability fails on OrbStack, continue only with frozen API fixtures explicitly marked `networkEvidenceMode=fixture_only`, or disable the add-on. `l7Context` depends on PoC results; `traceCompletion` remains disabled and `get_trace_context` returns `capability_disabled`.

## Alternatives considered

- Bundle `deepflow-app`, its GUI, Stella, ByConity, Jaeger, and the OTel suite: rejected because those components are outside the 1.0 standard delivery boundary.
- Query DeepFlow ClickHouse directly: rejected because it bypasses the stable Server/Querier interface and exposes storage credentials to platform code.
- Install agents on VM guests or every SSH host, or create additional central planes automatically: rejected because 1.0 limits collection to managed Kubernetes clusters and one central plane.

## Consequences

Network evidence depends on the selected Profile and PoC result. Fixture-based development must remain visibly distinct from live telemetry. The precise DeepFlow release lock and production high availability, RPO, and RTO remain open until their stated validation stages; this ADR accepts neither as completed validation.

## Implementation boundaries

- Do not claim DeepFlow v7.2.0 or live eBPF as validated before the OrbStack PoC passes.
- Do not fabricate live evidence when using fixtures or report unsupported trace context as available.
- Do not include `deepflow-app`, GUI, Stella, ByConity, Jaeger, or an OTel suite in standard 1.0 delivery.
- Do not provide direct ClickHouse business-query access to platform code.

## Rollback conditions

If the PoC fails, use only the specified fixture-only mode or disable the add-on. If a supported Server/Querier boundary cannot meet a documented requirement, stop DeepFlow integration and submit a new ADR with reproducible capability, license, and compatibility evidence before selecting another source.
