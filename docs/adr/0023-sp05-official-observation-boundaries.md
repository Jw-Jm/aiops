# ADR-0023: Official Kubernetes Metrics and collector availability observations

Status: implementation in progress; not final SP05 acceptance.

The existing Worker now reads the official fixed `GET /version`, exact Node GET,
and `metrics.k8s.io/v1beta1` NodeMetrics GET through the collector's existing
cluster limiter and credential transport. It selects one of all current admitted
Node identities per round. Registration, tenant, cluster, revision and immutable
source scope are checked before reads and before common ingestion. Metric names
are joined to the already admitted UID, with native Node UID, labels and capacity
rechecked before and after the metric read. A sample predating Node creation is
rejected. No command or free-form endpoint is introduced.

`CPUUtilizationHigh` and `MemoryUtilizationHigh` are fixed v1 symptoms for usage
strictly above 90% of current allocatable capacity. Exact Quantity decimal
comparison handles nanocores and byte units. They do not confirm causation and
are not performance measurements. Missing, stale, malformed or changed-identity
metrics cannot resolve an existing symptom. Complete native low samples may
resolve it. Optional Metrics availability is reported independently of the
necessary sources used by each Recipe.

`ControlPlaneUnreachable` is relative to this bound collector, attached to an
already admitted Node. HTTP 503/504, an issued request's connection refusal or
request timeout qualify; authenticated native `/version` recovery may resolve.
401/403, TLS, DNS, contract drift, throttling and local rate-budget failures are
unavailable observations, not proof of outage or recovery. A source withdrawal
prevents further API reads and ingestion.

The official [NodeMetrics reference](https://kubernetes.io/docs/reference/external-api/metrics.v1beta1/)
defines usage, timestamp and window. Manually labelled positive and negative
inputs were frozen with SHA-256 in `test/fixtures/inspection/kubernetes/observations-v1`
before implementation. The first red run records missing implementation, not a
previous business regression. Its 22 cases supplement the existing condition,
scheduling and storage labels; neither dataset is a production accuracy claim.

Actual OrbStack has no Metrics API (native GET returns 404). The live correctness
entry verifies that honest degradation, real Node identity, native `/version`,
controlled collector-side 503/403, recovery, source withdrawal, PostgreSQL
correlation and real encrypted archives. No shared control plane is stopped,
no Metrics service is installed, and no CPU or memory load is generated. The
positive Metrics path currently has protocol Fixture evidence only; it must not
be represented as positive live Metrics-server qualification.
