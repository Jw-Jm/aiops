# ADR-0004: SeaweedFS Evidence Archive

Status: Accepted

## Context and rationale

The platform must retain selected evidence beyond source-system retention while keeping the standard installation self-contained and offline-capable. Existing S3-compatible systems can be reused only through an explicit deployment choice; they are not a prerequisite for the standard bundle.

## Decision

For a new standard installation, bundle SeaweedFS as the S3-compatible Evidence Archive. Platform code uses the S3 API through AWS SDK for Go v2. Archive only the required evidence slices, command output, and test assets. Platform policy owns retention, digest, encryption, Legal Hold, and recovery behavior. An explicitly supported future Deployment Profile may select a compatible existing S3 service; this ADR does not make that service a default dependency.

## Alternatives considered

- Require an external S3 service: rejected because it makes a standard offline installation depend on pre-provisioned infrastructure.
- Add the MinIO SDK or modify SeaweedFS: rejected because the platform contract needs the S3 interface and the plan prohibits those dependencies and source changes.
- Store all archive objects in PostgreSQL: rejected because object archival is a separate S3-compatible storage responsibility.

## Consequences

The standard bundle includes SeaweedFS and its installation materials. Storage use must remain minimal and be covered by retention, digest verification, encryption, and recovery procedures. The use of SeaweedFS does not by itself provide regulatory WORM guarantees.

## Implementation boundaries

- Do not make external S3 a development or standard-bundle prerequisite.
- Do not add MinIO-specific APIs or modify SeaweedFS source.
- Do not claim native regulatory-grade WORM or absolute immutability; enforce platform append-only and retention rules separately.

## Rollback conditions

Reconsider the backend only if measured compatibility, recovery, security, licensing, or retention requirements cannot be met through the S3-compatible boundary. Record an accepted replacement ADR and demonstrate object migration, digest preservation, and recovery before changing the default.
