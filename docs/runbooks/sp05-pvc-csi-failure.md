# SP05 PVC/CSI provisioner Runbook

Scope: the nonvirtual `pvc-csi-failure` slice. Frozen native inputs, expected business identities/semantics and SHA-256 manifest are `test/fixtures/incidents/pvc-csi-failure/v3/`. Expected values were frozen independently of current business output; earlier versions and failure evidence are retained. No VM or KubeVirt/CDI capability is enabled.

Prepare the locked toolchain and owned PostgreSQL, TLS OpenBao Transit and role-separated TLS/IAM SeaweedFS dependencies, then source their private dependency environment files (never copy credentials into evidence). Run from platform:

```sh
OPS_PERFORMANCE_EXEMPTION=sp05-user-20261002 go test ./test/integration -run '^TestSP05FrozenNonvirtualGoldenVerticalAndReplay/pvc-csi-failure/' -count=1 -v
```

The actual native adapters and unified Finding ingestion must reach the durable outbox consumer, Incident, signed Registry Recipe, immutable Evidence archives, append-only RCA and bounded SP04 Ariadne/ontology Impact. The same fixed input is replayed twice including complete reducer/post-check execution. Valid evidence confirms only the declared required native predicate; conflict, missing required evidence or degraded necessary source remain unresolved/partial. Every RCA Evidence ID is read back and checked against its content digest and real 365-day dependency/object protection.

Use the authenticated Findings/Incident/timeline/Evidence/RCA endpoints and scoped resource impact endpoint to inspect results. `GET /api/v1/incidents/{id}/rca` preserves frozen revision while separately reporting currentEligible/currentDegradedSources; it must not reuse historical graph freshness when the current owner/source or Recipe is unavailable. Historical reads still require current tenant/resource/source authorization. New pushed observations require explicit trusted publisher binding and `/api/v2/findings:ingest`; the historical v1 endpoint is disabled.

Recovery: verify source revision/backend/scope, current Graph owner/lease, active signed Recipe and real archived Evidence. Retry transient delivery/archive faults through the same existing Worker; malformed or historical payloads remain preserved deadletters. Never force a confirmed status, fabricate evidence or reset a shared environment. Clean up only the test namespace/bucket/container whose UID/identity and owner label match this run.

Validation limits: these frozen hardware/kernel/CSI inputs are protocol Fixtures, not physical BMC, hardware or Node fault injection. Native Kubernetes inspection and Linux original Analyzer are separately exercised in owned OrbStack resources. Positive native Metrics-server capability is unverified on this cluster; its missing-data degradation and versioned positive protocol response are tested. Dedicated throughput/capacity/P95 gates are waived, not passed. See `docs/evidence/sp05-20261002/task-ledger.md` for current commands, failures and successful regressions.
