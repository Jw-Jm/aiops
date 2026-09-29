# Kubernetes ontology diagnostic and evidence baseline

Status: `candidate`. Task 2.8 completed a minimal, test-only adaptation PoC against the locked Ariadne graph. The PoC establishes that the selected upstream diagnostic semantics can read from one Ariadne graph without creating a second node or edge index. It does not implement or qualify a production graph service.

## Source selection and license

- Repository: <https://github.com/Colvin-Y/kubernetes-ontology>
- Release line: `v0.1.8`, commit `346236312685a2e2e824c46b9ecdb3e8e8a10c76`.
- Source archive SHA-256: `7b579f52dacdbc2f3bb44c4a6c0623dc499de9d59ea1cc22fad562e0b5982e9f`.
- License: Apache-2.0. The root `LICENSE` and `NOTICE` are preserved under `third_party/licenses/kubernetes-ontology/`. Selected files and individual hashes are in `docs/poc/graph-reuse-lock.yaml`.
- Selected packages: `internal/api`, `internal/model`, `internal/graph`, and `internal/service/diagnostic`. No upstream server, `Kernel`, or second graph index is deployed or imported into the platform runtime.

## Adapter PoC and dependency closure

The exact patch `test/fixtures/upstream-graph/ontology-ariadne-reader.patch` makes the smallest boundary change in the temporary upstream checkout: `diagnostic.Service` accepts a three-method graph reader interface (`GetNode`, `ListNodes`, `Neighbors`) rather than a concrete `*graph.Kernel`. The fixture test implements that interface directly over Ariadne's `Graph` methods. It holds only a pointer to the Ariadne graph and creates no parallel node, edge, or adjacency maps.

The patched upstream source and test are replayable against the locked commit. The patch also aligns the selected ontology Go dependencies to Kubernetes module version `v0.35.2`: the original `api` and `client-go` `v0.30.1` did not compile with `apimachinery` `v0.35.2` and Ariadne's structured-merge-diff v6 dependency. The change is limited to this isolated test PoC; it does not alter platform production modules. The patch and the 40-module Go test closure, including module checksums, archive digests, license expressions, license-file hashes, and notices, are locked in `test/fixtures/upstream-graph/ontology-adapter-go-dependency-closure.json` and `docs/poc/graph-reuse-lock.yaml`. Every module in that closure has identified license evidence.

At the exact locked upstream commit, the patched graph and diagnostic packages pass their tests. The adapter test exercises:

- ownerReference, Service selector, Pod scheduling, and Pod → PVC → PV → StorageClass → CSIDriver relations;
- depth, node, and edge budgets through the ontology diagnostic service;
- a node-budget cutoff that returns `Partial` with the expected `maxNodes` truncation reason;
- distinct Ariadne instances with the same resource IDs, snapshot replacement using a new graph, and stale-edge removal after an update;
- TTL-style expiry of a temporary DeepFlow overlay while retaining a hardware-to-Node edge in the same graph.

The selected upstream and adapter replay logs are `artifacts/test-reports/task-2.8-ontology-adapter-upstream.log` and `artifacts/test-reports/task-2.8-ontology-adapter-patch-replay.log`. The patch's initial compile failure against the concrete Kernel constructor is preserved in `task-2.8-ontology-adapter-red.log`; after the interface boundary patch the focused test passed in `task-2.8-ontology-adapter-green.log`.

## Public Contract projection

The actual ontology `DiagnosticSubgraph` result is captured as a deterministic golden fixture at `test/fixtures/upstream-graph/ontology-ariadne-diagnostic.golden.json`. `test/contract/ontology_projection_test.go` maps that fixture into the platform `diagnostic-graph/v1` schema and validates it with the platform Contract validator. The test also exercises a synthetic degraded-source projection, asserting `partial`, freshness, warning, and degraded-source fields. This is a Contract projection fixture, not a live source outage test.

The projection normalizes upstream relation names and provenance labels, supplies the platform tenant identity and freshness fields, and preserves the selected diagnostic nodes, edges, and query budgets. The public Contract is the boundary; no upstream ontology type is exposed as a platform API.

## Decision and limits

The ontology diagnostic semantics pass the Task 2.8 test-only adapter gate with Ariadne as the only graph index. The ontology source remains `candidate` and outside the Bundle. Production tenant routing, durable lifecycle and failure handling, production TTL, and runtime capacity qualification are still outside this baseline. This is the explicit route forward for SP-04 to consume; it does not implement SP-04.

KubeVirt/CDI relations and runtime evidence remain deferred under ADR-0008 and unverified. The conformance inputs in this phase contain only non-virtual Kubernetes, storage, hardware, and DeepFlow facts.
