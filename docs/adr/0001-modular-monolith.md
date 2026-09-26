# ADR-0001: Modular Monolith

Status: Accepted

## Context and rationale

The 1.0 control plane needs shared domain rules and transactional coordination for findings, incidents, investigations, and asynchronous work. Splitting those responsibilities into network services now would add deployment and contract boundaries before the product has validated them. The API and background worker still have different process and scaling needs.

## Decision

Use one Go Module with explicit internal domain modules. Build and run `platform-api` and `platform-worker` as separate processes that share domain packages and the platform database. `incident` remains an internal domain module. Preserve transactional Outbox and idempotent consumers so a later service split can be evaluated against stable boundaries.

## Alternatives considered

- Split each domain into microservices: rejected for 1.0 because it adds network contracts and operational components without a validated need.
- Run API and worker in one process: rejected because their runtime and scaling responsibilities differ.

## Consequences

The codebase has one module and a coordinated release, while API and worker processes can be deployed and scaled separately. Internal domain calls stay simpler and transactional workflows can use the shared database. A future split will require explicit contracts and migration work.

## Implementation boundaries

- Do not deploy `incident` as an independent service or add internal REST/gRPC calls between platform modules.
- Keep module ownership explicit; use Outbox and idempotent-consumer boundaries for asynchronous handoffs.
- This decision does not accept or define production high availability, disaster recovery, or a production Kubernetes compatibility target.

## Rollback conditions

Reconsider the deployment shape only if measured 1.0 requirements show that separate API and worker processes plus the existing module boundaries cannot meet a documented isolation, scaling, security, or availability requirement. A replacement must be recorded in a new accepted ADR with migration and compatibility evidence before service boundaries are introduced.
