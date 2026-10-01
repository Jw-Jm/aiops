# ADR-0016: Archive TLS and tenant IAM principals

Status: Accepted

## Context and rationale

The frozen operations specification (08, Evidence Archive security boundary) requires TLS, a separate prefix plus IAM for each tenant, and rejection of cross-tenant list/get. The original Worker held a single bucket-wide S3 credential. Application key validation was present but did not establish the server IAM boundary.

## Decision

Use the existing AWS SDK and S3-compatible backend. Each tenant receives a distinct operator-provisioned S3 IAM principal limited to `archive.TenantPrefix(tenant_id)`. The Worker reads a bounded, strict `ops-archive-credentials/v1` JSON Secret containing tenant UUID/access key/secret key entries and routes only to the principal bound to the validated object prefix. There is no global credential fallback, including on missing provisioning or outage.

Before using a principal, verify own-prefix listing succeeds and unscoped/foreign-prefix listing and foreign object reads return S3 AccessDenied/403. A timeout, missing object/404 or successful empty listing is not proof. Repeat the check after one minute; failed checks are retried rather than cached. Missing/invalid tenant configuration leaves the tenant's audit segments pending and reports degradation.

S3 outside literal loopback test fixtures requires HTTPS with an independently supplied public CA and endpoint hostname verification. Bundled SeaweedFS serves TLS on its existing 8333 port using operator TLS and IAM configuration Secrets. Bootstrap credentials are separate from Worker credentials and cannot pass tenant IAM verification.

## Alternatives considered

- Rely on the application's prefix check with one global principal: does not satisfy the server IAM requirement.
- Implement a second object store or modify SeaweedFS: unnecessary and prohibited by ADR-0004.
- Automatically create IAM users through a privileged runtime credential: increases runtime privilege. Provisioning remains an operator/bootstrap operation.

## Consequences

New tenants need corresponding IAM and runtime Secret entries before archival. Rotate keys by provisioning the new restricted principal and restarting the Worker with the updated Secret; retire old keys after pending work and verification succeed. Do not relax the principal or fall back to bootstrap credentials when a tenant is unconfigured. Runtime startup does not create buckets, change retention, administer IAM or modify existing installations.

The isolated pinned SeaweedFS 4.47 tests exercise TLS, separate principals, own-prefix round trips, server cross-prefix rejection and global-principal rejection. They establish the development contract; they do not claim production HA, arbitrary S3 backend compatibility or regulatory WORM certification.

## Implementation boundaries

Tenant credential/TLS private material stays outside Git, Bundle, reports and logs. The Chart mounts the tenant map only in the Worker; it does not serialize credentials into its generated public ConfigMap. The installer checks referenced Secret keys before image import without retrieving their values. External backend policies must satisfy the same probes. KubeVirt/CDI and SP-04–SP-09 remain outside this change.

## Rollback conditions

On IAM/TLS incompatibility fail closed and preserve pending audit state. Restore only a previously valid restricted credential configuration through the operator procedure. Replacing S3 or changing the frozen isolation requirement requires a separate specification decision; a shared credential is not a rollback option.
