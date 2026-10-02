# SP-04 nonvirtual Resource and Evidence runtime

SP-04 is explicitly authorized by the user and ADR-0018. Apply forward-only
migrations 00018–00023 using the migration identity before enabling the updated
API/Worker. ADR-0008 still disables KubeVirt/CDI and all VM/VMI/DataVolume work.
The foundation credentials and tenant IAM requirements in
[SP-03 runtime](sp03-foundation-runtime.md) remain prerequisites.

## Process and credential boundary

The existing Worker owns the Ariadne/ontology memory graph. The API calls the
Active Worker over the versioned internal graph protocol, mutually authenticated
TLS, a fresh workload CRL and a signed, short-lived effective authorization scope.
There is no separate graph service. Use exactly two Worker replicas; each shares
one QPS<=10/burst<=25 budget across tenant credentials for a native cluster. Lease,
probe and List/Watch calls consume that same budget.

Enable `sp04.enabled` in the chart with the identity/source Secret names, public
CA/CRL certificates, expected Worker server name, archive logical backend ID,
explicit Worker CIDRs and API-server control-plane CIDRs. API identity Secrets
contain the context private key; Worker identity Secrets contain only its public
key. The source configuration/credentials Secret is mounted only in Worker.
`SP04_RUNTIME_FILE` is strict JSON described by `internal/app.SP04Config`:
`Clusters` binds tenant, native kube-system UID, source ID, source revision,
immutable logical backend, native HTTPS endpoint, CA/token file and the original
Lease namespace/name. `Sources` binds each fixed Adapter to the same registered
revision/backend/dataScopeMapping and an explicit positive ScopeProbe. No secret
belongs in Helm values, evidence logs or command-line arguments.

Preprovision each configured Lease with the owner-epoch annotation `0` before
first use. Normal Worker RBAC grants only get/update on that exact Lease name;
create/delete is not part of collection. Kubernetes Lease is the only authority.
Database graph_ownership rows are expired routing/observation mirrors on failure.
Source query capability stays disabled until the actual Adapter completes both
its positive scope canary and negative isolation probes. Empty mappings do not
mean unrestricted access. If those proofs expire or credentials/registration
change, the Adapter rejects queries and reports degradation.

## Lease deletion/recreation recovery

A deleted or changed Lease UID fails closed. Stop both affected Workers before
operator repair. Preserve their configured tenant, cluster UID and original Lease
namespace/name. Obtain the retained Lease UID/epoch from the routing mirror and
confirm the authoritative kube-system UID. If the retained epoch floor is missing,
restore its audited backup; do not reset it to zero or infer ownership from a DB
row. Restore credentials/connectivity first when the API server is unavailable.

Prepare a private mode-0600 JSON file with `DatabaseURL` (a dedicated login granted
only worker_runtime_role), `Endpoint` (native HTTPS API server), `CAFile`,
`TokenFile` (operator Kubernetes credential) and `Request` containing `Tenant`,
`Cluster`, `Namespace`, `Name`, `ExpectedPreviousLeaseUID`, `Ticket`. The operator
needs SelfSubjectReview creation, get on kube-system namespace and get/update on
the original Lease plus create in that Lease namespace if it was deleted. These
additional privileges must not be granted to the ordinary Worker identity.

Run `opsctl graph lease-recover --config <private-json>`. The command derives the
operator subject from native authentication, checks cluster identity and any live
holder, commits a requested audit record, then creates or CAS-repairs the native
Lease at an epoch above both retained and existing authority floors. The receipt
contains only the intent ID, new Lease UID and epoch floor. It expires the routing
mirror and appends an applied audit record; it never elects a Worker. Restart both
Workers and wait for all 17 required nonvirtual GVRs to complete authoritative
initial Watch snapshots. Only a qualified Worker may claim a strictly newer epoch.
Old responses, cursors and caches must be rejected.

If native repair succeeds but the database receipt fails, the command reports
`authority repaired; audit receipt pending`; routing remains unusable. Restore DB
availability, inspect the native recovery-intent/epoch and requested audit record,
then retry with the still-retained previous UID. The retry advances the native
floor again and records a new intent; the original requested record remains an
explicit unapplied attempt. Never treat that original attempt as applied. A stale
previous UID after a completed repair is rejected. Preserve both audit attempts.

## Evidence and lifecycle

All semantic queries use fixed versioned templates, explicit bounded parameters
and mandatory server-side scope mapping. DeepFlow uses only its locked Querier
API and frozen Canonical endpoint mapping; its current admitted mode is
`fixture_only`, L7 remains disabled and Trace is `CAPABILITY_DISABLED`. Redfish
reads only inventory through Gofish and selected admitted hardware model/rules;
fixture qualification does not qualify a whole component distribution.

Collection commits a pending intent and Transit ciphertext before acknowledging
short-lived facts. Upload recovery verifies immutable object version, ciphertext
and plaintext digests and retained key version before `archived_verified`.
Pending query intents store bounded parameters, never raw full telemetry, and may
replay only the exact original fact digest. Expired unencrypted sources become
`unavailable`. Keep all referenced Transit versions for the full retention life.

New archive objects receive at least 366 days of physical retention because
capture creates a 365-day audit dependency; the base Evidence metadata retention
remains 180 days. Cleanup checks the full reverse reference closure, active
Incident, Action/Audit/archive refs and Legal Hold before durable deleting state.
Legal Hold writes commit logical protection atomically and return protectionSync
`pending`; Worker maintenance extends/holds the exact physical object and records
verified receipts. Monitor archive write/protection failures and pending intents.
No metadata-only operation may claim verified physical protection.

Current acceptance commands, failure evidence and independent review records are
tracked in [SP-04 ledger](../evidence/sp04-20261001/acceptance.md). Historical reports
are not proof for the current source. Local samples do not establish production
capacity, high availability or postponed virtualization acceptance.
