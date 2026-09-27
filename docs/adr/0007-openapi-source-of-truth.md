# ADR-0007: OpenAPI as the Public API Source of Truth

Status: Accepted

## Context and rationale

The platform exposes REST operations to the web client and other authorized consumers, while Go and TypeScript need matching request, response, and event types. Maintaining parallel handwritten contracts would allow those clients to drift.

## Decision

Maintain public REST contracts in OpenAPI 3.1 as the single source of truth, with `/api/v1` as the base path. Generate Go API bindings with `oapi-codegen` and TypeScript clients and types with Orval from that source. Define SSE event envelopes in the same API contract. Keep platform-owned public schemas independent of upstream private schemas. Domain models remain internal and are mapped at the HTTP boundary.

## Alternatives considered

- Treat handwritten server handlers or Go structs as the source and maintain client definitions separately: rejected because it creates multiple contract authorities.
- Introduce GraphQL or internal gRPC as another API contract: rejected because 1.0 specifies versioned REST and SSE with in-process module boundaries.

## Consequences

Public API changes begin in the OpenAPI document and regenerate client/server bindings. Contract review can cover routes, request and response shapes, errors, and SSE events before implementation. Generated files must stay reproducible and must not become a second editable source.

## Implementation boundaries

- Do not hand-edit generated bindings or expose an upstream project's private schema as the public platform API.
- Do not create a parallel GraphQL contract or internal gRPC service boundary.
- This ADR records the source-of-truth decision only; the OpenAPI document and generated code are delivered by their later implementation task.

## Rollback conditions

Reconsider OpenAPI 3.1 only if a documented contract requirement cannot be represented or maintained by the selected generation workflow. A new accepted ADR must define a single replacement source, conversion plan, and compatibility checks before the source of truth changes; parallel authorities are not an acceptable transition state.
