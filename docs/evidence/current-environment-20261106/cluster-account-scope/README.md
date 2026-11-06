# Cluster account scope follow-up

This follow-up ran against the deployed `dd9da1cca0f87d5bd943faee74f883648c916413` API, Worker, and Web artifacts at `https://192.168.0.63:30471`.

## Accepted checks

- Authorization UI exposes only operator, platform administrator, and administrator/operator.
- The operator grant is cluster-wide for the registered cluster; foreign-cluster reads return 403.
- Kubernetes, metrics, and logs sources are registered for the same 31-namespace scope at revision 2.
- CoreDNS returns real rows for CPU, memory, restart, and phase metrics.
- Cluster log discovery uses two bounded cluster-level list requests while retaining source, authorization, canonical-ID, response-size, pagination, and 200-result guards.
- The final log query returns 20 redacted samples and a durable query result.
- Incident `01a1ae6b-7939-7010-845d-59eb01ca3a27`, created after source revision 2, returns detail, timeline, evidence, and RCA.
- After recording the release, the formal final smoke returns 200 for resources, findings, incidents, execution profiles, and all capability endpoints; all primary pages load and graph is ready without source degradation.
- A 30-minute post-release observation records no API/investigator errors or application restarts. Worker retains known slow graph-lease warnings and three fixed-analyzer warnings while remaining available.

## Preserved failures and limits

- Incident `01a1ae35-7bdd-7af0-93e7-3d5c93f6fea4` was created against source revision 1. Its detail and timeline remain readable, but evidence/RCA reads are rejected after the source moved to revision 2. This is the intended source-revision fence, not a successful evidence read.
- `unit-406159e-known-failures.log` and `unit-dd9da1c.log` preserve three failures in bundle migration admission, fault-chain relation scope, and DIMM impact scope. They reproduce identically on parent commit `406159e`; `dd9da1c` changes only `internal/app` observation query files.
- The web unit suite passes all 267 assertions, but Vitest exits nonzero from three existing asynchronous `window is not defined` teardown errors.
- Earlier log scripts omitted `clusterUid` from one POST path and correctly received 403. The final smoke and final log scripts shown under `../tools/cluster-account-scope/` use the scoped path; the false script failures are not counted as product failures.
- Source follow-up fixes after this release synchronize migration admission digests, fence fault-chain relation reads to selected namespaces, narrow DIMM impact expansion to cluster objects plus selected namespaces, and drain the Ant Design button timer before test teardown. They pass the standard Go unit entry and all 267 web assertions, but are not part of the deployed `dd9da1c` release.
