# Ariadne resolver baseline

Status: `candidate`. The pinned upstream resolver and a test-only ontology adapter pass the non-virtual Task 2.8 conformance checks. This is not production graph-service qualification and does not add Ariadne to a Bundle.

## Source and license lock

- Repository: <https://github.com/aalpar/ariadne>
- Commit: `fa2232ce45c4c869e47ddae1c57e8886830cfb6c` (`v0.1.2-12-gfa2232c`)
- Go module: `github.com/aalpar/ariadne v0.1.3-0.20260310221605-fa2232ce45c4`
- SHA-256 of the source archive: `641737135cb62446d0eefd27add051fd2be2f7843bcacdaaf3e2b05f7ea375ba`
- License: Apache-2.0. The source license is preserved at `third_party/licenses/ariadne/LICENSE`. Exact selected files, Go module checksums, archive digests, and license evidence are recorded in `docs/poc/graph-reuse-lock.yaml`.
- Ariadne is imported by tests only. No production API or worker imports it.

## Non-virtual conformance

`test/contract/upstream_graph_nonvirtual_test.go` builds the pinned library over a deterministic 20,000-object input whose generator explicitly excludes VM/VMI/DataVolume kinds. The fixture metadata file SHA-256 is `3a9b1140ace9234cb17697da8cacdc05e185d5a9e2b2933829552f4334988a3a`; its generated object array SHA-256 is `0a4ed067fd482112d0b1d0b6281d6cc481dad488183b09b4eae2730fca9de8f2`. The test exercises:

- `NewStructuralResolver`, `RefRule`, and `LabelSelectorRule` for owner references, Services/selectors, Pod/PVC/PV/StorageClass/CSIDriver relations, and updates that must clear stale edges;
- direct `DependenciesOf` lookups as the bounded query path;
- a versioned HardwareNode-to-Node rule and a temporary DeepFlow FlowEndpoint-to-Pod rule in the same Ariadne graph;
- removal of an object and its edges.

The deterministic input and its exact object digest are in `test/fixtures/upstream-graph/nonvirtual-golden-input.json`. On the host, Ariadne loaded the 20,000 objects in 47.38 seconds. The static Linux arm64 test binary also ran in the local OrbStack Docker engine with `--network none`, `--pull never`, a read-only checkout, non-root UID, dropped capabilities, and no Kubernetes resource access; graph loading took 46.23 seconds. These measurements satisfy the requested development regression; they are not production capacity guarantees.

The separate adapter PoC in `test/fixtures/upstream-graph/ariadne-ontology-adapter-poc_test.go.fixture` verifies tenant-instance separation for duplicate IDs, replacement by rebuilding into a fresh graph, bounded ontology subgraphs, removal of expired DeepFlow overlays, and preservation of hardware edges. Ariadne's `Load` is additive, so a replacement snapshot must use a fresh graph instance.

## Scope and decision

The earlier mixed fixture and `upstream_graph_test.go` are preserved as prior work. This acceptance used only the non-virtual fixture. KubeVirt/CDI relation adaptation remains deferred under ADR-0008 and is unverified.

Ariadne remains `candidate`; it is not selected by the core Profile or admitted to the Bundle. Task 2.8 has established a replayable, test-only route for the non-virtual graph and ontology baseline. Runtime graph construction, durable tenant/cluster routing, production TTL and failure handling, and the SP-04 service remain outside this PoC.

## Executable non-virtual replay

`TestGraphNonVirtualUpstreamReplay` in `test/contract/graph_replay_test.go`
reconstructs both frozen commits with `git archive`, checks archive and selected
file hashes, applies the locked ontology patch, and runs a newly compiled
20,000-object test binary and the upstream ontology graph/diagnostic tests in
the digest-locked Linux arm64 Go container. Both containers use the explicit
OrbStack context, `--network none`, `--pull=never`, read-only source/module
mounts and dropped capabilities. No cluster resources or virtual fixtures are
used. The source checkouts and module cache must already be available; there is
no download fallback.

```sh
OPS_GRAPH_REPLAY=1 OPS_GRAPH_SOURCE_DIR=/absolute/prepared/upstream \
GOPROXY=off GOSUMDB=off \
go test ./test/contract -run '^TestGraphNonVirtual(ReplayIsLocked|UpstreamReplay)$' \
  -count=1 -timeout=20m -v
```

The source directory must contain the `ariadne` and `kubernetes-ontology` Git
repositories with the frozen commits. `OPS_GRAPH_MODULE_CACHE` can point to a
prepared read-only module cache; otherwise the active Go module cache is used.
The default suite checks the locks and skips this explicitly requested replay.

## Exit conditions

Retain the frozen version while its licensed source and conformance results
remain valid. Any proposed replacement must pass the same owner/selector,
storage, update/removal, isolated-instance and 20,000-object tests before
changing the reuse lock. Failure to preserve bounded queries, stale-edge
removal or tenant/cluster isolation stops the affected Task 4.3/5.5 work and
requires an ADR with the failing input and upstream evidence. It does not
authorize a platform graph engine or a second adjacency store. Public graph
contracts remain platform-owned during that decision.
