# Cluster account scope follow-up

This follow-up accepted and recorded `b3d7208b5410f72bc4adc22a0e0423e3f6e0ab3f` at `https://192.168.0.63:30471`. The previous `dd9da1cca0f87d5bd943faee74f883648c916413` artifacts established the source and runtime baseline; `b3d7208b5` adds source-scoped fixes for fault-chain relations, DIMM impact, migration admission digests, and Web teardown timing.

## Accepted checks

- Authorization UI exposes only operator, platform administrator, and administrator/operator.
- The operator grant is cluster-wide for the registered cluster; foreign-cluster reads return 403.
- Kubernetes, metrics, and logs sources are registered for the same 31-namespace scope at revision 2.
- CoreDNS returns real rows for CPU, memory, restart, and phase metrics.
- Cluster log discovery uses two bounded cluster-level list requests while retaining source, authorization, canonical-ID, response-size, pagination, and 200-result guards.
- The final log query returns 20 redacted samples and a durable query result.
- Incident `01a1ae6b-7939-7010-845d-59eb01ca3a27`, created after source revision 2, returns detail, timeline, evidence, and RCA.
- The b3d final log query returns 20 redacted samples, and foreign-cluster incident checks return 403.
- The first b3d final smoke returned 503 in the Worker graph cold-start window; `smoke-b3d7208b5-cold-start-failure.json` preserves that failure and `smoke-b3d7208b5-retry.json` preserves the passing retry.
- After recording the release, the formal final smoke returns 200 for resources, findings, incidents, execution profiles, and all capability endpoints; all primary pages load and graph is ready without source degradation.
- `go vet`, the Go unit suite, the Web unit suite, and the Python 3.12 contract suite passed for the accepted source revision.
- Worker remains available. Graph Lease warnings trace to shared `/data` synchronous commit queueing, not CPU/memory limits or restarts; see `worker-warning-investigation.md`.

## Preserved failures and limits

- Incident `01a1ae35-7bdd-7af0-93e7-3d5c93f6fea4` was created against source revision 1. Its detail and timeline remain readable, but evidence/RCA reads are rejected after the source moved to revision 2. This is the intended source-revision fence, not a successful evidence read.
- `unit-406159e-known-failures.log` and `unit-dd9da1c.log` preserve three failures in bundle migration admission, fault-chain relation scope, and DIMM impact scope. The accepted b3d regression suite passes those targeted cases.
- Earlier log scripts omitted `clusterUid` from one POST path and correctly received 403. The final smoke and final log scripts shown under `../tools/cluster-account-scope/` use the scoped path; the false script failures are not counted as product failures.
- The fixed Analyzer emitted three warnings: two during graph warm-up and one redacted analyzer failure at 02:46:14Z. Current logs intentionally retain only the error class, so the third failure is not claimed as diagnosed; it did not recur in the observed window.
- DIMM impact and fault-chain fixes are source-level regressions only; no hardware live failure was replayed. Command execution, virtualization, Trace, production/HA, and empty-cache offline Bundle acceptance remain outside this record.
