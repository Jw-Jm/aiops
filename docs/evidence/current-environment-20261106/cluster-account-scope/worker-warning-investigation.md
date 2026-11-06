# Worker warning investigation for b3d7208b5

Investigation window: `2026-11-06T02:43:34Z` through `2026-11-06T02:51:30Z`, after the Worker recreation and before the final current-record smoke.

## Graph Lease renewal

- 74 renewals exceeded the 300 ms total threshold. The elapsed distribution was p50 457 ms, p90 1101 ms, p95 1218 ms, and max 1835 ms.
- Every total-threshold warning ended in `route_mirror_write`; 43 stage warnings identified `lease_cas` and two identified `lease_read`. `lease_cas` stage time was 200-254 ms.
- The route mirror is a PostgreSQL tenant transaction: begin, `set_config`, one hot upsert into `platform.graph_ownership`, and synchronous commit. The table had 4848 hot updates during the environment lifetime.
- The Worker cgroup recorded only 10 throttled periods totaling 21.798 ms over 4691 periods and about 47 MiB resident memory. Pod restart count was zero. CPU and memory limits are therefore not the cause.
- PostgreSQL cache hit rate was effectively 100%, but `fsync` and `synchronous_commit` were enabled. During a 15-second sample the database committed 213 transactions and updated 159 tuples. `/dev/sda1` showed 82.95 ms write await, 56.56 ms fsync await, average queue depth 5.47, and 151% utilization. PostgreSQL had written about 2.26 GiB and the log container had read about 2.07 GiB.

Conclusion: the warning is genuine storage-side latency from synchronous database commits sharing a saturated `/data` block device. The 200 ms Kubernetes Lease CAS baseline also sits above the stage warning threshold. No threshold was relaxed and no warning or failure evidence was deleted.

Follow-up options are to isolate or accelerate `/data`, review non-critical write cadence and batching, and retain transaction-stage metrics. Those changes require separate review and acceptance; this document does not claim they were applied.

## Fixed Analyzer

Three `SP05 fixed Analyzer unavailable` warnings were retained:

- `02:43:39Z` and `02:44:09Z` occurred while the graph was not yet qualified after Worker recreation.
- `02:46:14Z` was a redacted analyzer failure. The production logger intentionally emits only the error class, so the exact reason cannot be reconstructed without guessing. It did not recur during the remainder of the observation window, and final smoke reported graph ready with no source degradation.

If persistent diagnosis is required, add a non-sensitive structured reason code at the analyzer boundary; do not log raw stderr or credentials.
