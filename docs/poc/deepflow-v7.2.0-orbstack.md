# DeepFlow v7.2.0 OrbStack PoC

**Current result (2026-09-29): `live`, exit 0.** The final source-bound working
implementation passed actual Agent registration, generated MySQL traffic,
standard Querier L4 API queries and public TCP-denial probes in all four
runtime Pod network namespaces. L7 context and trace completion remain false.

**Historical result before the policy controller was installed:** `fixture_only`. The minimum services reached Ready with the pinned arm64 image digests, but OrbStack did not enforce the release-scoped deny-egress NetworkPolicy. The Agent registration and live Querier flow query were therefore not used as qualification evidence. This report does not claim live network evidence, L7 context, or production compatibility.

## Locked inputs

| Item | Exact lock | Source / license |
| --- | --- | --- |
| DeepFlow source | v7.2.0, commit `e567b167453ffa99f08f26def20379b4f831e073` | `https://github.com/deepflowio/deepflow`; Apache-2.0 in the pinned source tree |
| Upstream chart | `deepflow` 7.1.002, SHA-256 `ca0ce1e919cd1a4184e6fba5e41ce1935694557fd78a4b04883f036de288d519` | `https://deepflowio.github.io/deepflow/deepflow-7.1.002.tgz`; chart source commit `af3138f1815d392922cabb81f064b3b91061fc96` |
| DeepFlow Server | `docker.io/deepflowce/deepflow-server:v7.2.0@sha256:609d1f8a2020a750a9a34dbb7fff4b785cbe9b78549d86bc7cefb5bccf70b939` (`linux/arm64`) | v7.2.0 source commit above; Apache-2.0 |
| DeepFlow Agent | `docker.io/deepflowce/deepflow-agent:v7.2.0@sha256:25419423b6ea1c73897f5ed09a244f9172acadd95cec5ac1bf07a1ba3ed31bf9` (`linux/arm64`) | v7.2.0 source commit above; Apache-2.0 |
| MySQL | `docker.io/library/mysql:8.0.39@sha256:a1f202a397782b7e2a98359b3f476132189b179e04f9123d4750d527e937f138` (`linux/arm64`) | `https://github.com/mysql/mysql-server`, commit `d69a12a94538d2d17c0cc45abedd57648a7b2ebd`; GPL-2.0-only |
| ClickHouse | `docker.io/clickhouse/clickhouse-server:23.10.4.25@sha256:757180f5d4efcf4a29a3147ac9f0b58b8986a42adaa79febcd70b57ea68eac76` (`linux/arm64`) | `https://github.com/ClickHouse/ClickHouse`, commit `330fd687d41e2b6a3adb8920bcb2a39c1330e9af`; Apache-2.0 |
| Egress test helper only | `docker.io/library/busybox:1.36.1@sha256:bd44eb136a95dcc8dc58995e43abc40a413f2e8e3d4a2aae6bccbe94686acb05` (`linux/arm64`) | Test probe only; not a DeepFlow runtime dependency |

The official chart index did not provide a 7.2.0 chart. The PoC pins chart 7.1.002 separately from the v7.2.0 Server and Agent images; compatibility is not inferred from Pod readiness. The rendered runtime manifest contains no Redis image or Redis dependency. Grafana, Stella, ByConity, Jaeger, OTel, and `deepflow-app` are excluded. The upstream `deepflow-app` Deployment and Service templates were removed from the generated manifest, and its Querier configuration entry was removed.

The final manifest uses one Server, one MySQL, one ClickHouse StatefulSet, and one Agent DaemonSet. All DeepFlow Services are ClusterIP. Usage reporting is disabled; flow metrics and flow logs have six-hour retention. The MySQL credential and Server configuration are Kubernetes Secrets; the checked-in manifest contains only a replacement token. MySQL init SQL containing credentials was removed, with remote root access configured through the MySQL image's `MYSQL_ROOT_HOST` startup option. ClickHouse and MySQL storage claims are confined to the labeled PoC namespace.

## Historical OrbStack observation

The tested node reported Kubernetes `v1.35.6+orb1`, Linux arm64, kernel `7.0.14-orbstack-00374-gbbca68e8d741`, and container runtime `docker://29.4.0`. The `deepflow` namespace was absent before this PoC. The final install created the expected Agent, Server, MySQL, and ClickHouse Pods; they reached Ready using the digest-pinned images already present on the node. The final installed manifest and cluster contained no `deepflow-app` workload; the upstream raw chart emits that workload, which was excluded from the final manifest. The isolated namespace had no egress allow rule except same-namespace traffic and DNS to `kube-system`.

The actual egress probe used the pinned BusyBox image, carried the same `ops.platform.io/test-release=sp02-task-2-5` label selected by the NetworkPolicy, and attempted `http://1.1.1.1/`. Its captured output was:

```text
wget: note: TLS certificate validation not implemented
public-egress-connected
```

The probe exited 10 after the public request connected, despite the applied egress policy. The machine has only CoreDNS and the local-path provisioner in `kube-system`; no separate NetworkPolicy controller Pod was present. This is direct evidence that this OrbStack environment cannot meet the PoC's no-public-egress prerequisite using the supplied NetworkPolicy. The raw structured observation is frozen at `test/fixtures/deepflow/v7.2.0/orbstack-networkpolicy-egress-failure.json`.

Because the isolation prerequisite failed, this run stopped before querying `/v1/vtaps/` for Agent registration or posting an L4 flow query to `/v1/query/`. No live dependency row, retransmission counter, L7 context, or trace was observed. The separate `querier-show-tables-upstream.json` fixture is explicitly copied from the pinned v7.2.0 upstream Querier README and only describes the documented response envelope; it is not represented as a live query sample.

## Historical capability and admission result

```yaml
networkEvidence: true
networkEvidenceMode: fixture_only
l7Context: false
traceCompletion: false
```

`networkEvidence: true` records that a reproducible PoC observation exists; `fixture_only` makes clear the platform has no live network-evidence qualification from this run. DeepFlow remains `candidate`. The full transitive software/license closure and GPL-specific distribution review are not marked qualified; this PoC does not promote the Component Catalog entry or establish production support.

## Current reproduction

```sh
helm lint /tmp/deepflow-chartpkg/deepflow --values deploy/addons/deepflow/values-dev-arm64.yaml
OPS_DEEPFLOW_ORBSTACK_POC=1 go test ./test/e2e -run '^TestDeepFlowPOC$' -count=1 -v -timeout=30m
```

The live E2E now fails on isolation, registration or query errors. Exit 0 requires real Agent registration, application traffic, a valid Querier response and actual runtime Pod network probes. Cleanup selects only resources carrying the Task 2.5 release label, including its Lease.

## Exit plan

Keep DeepFlow optional and candidate outside the core Bundle. The current
bounded live PoC does not qualify its full software/license distribution
closure or production compatibility. A future runtime/Bundle admission must
complete those separate obligations. Isolation, source/digest, Agent or query
failure disables live evidence; the preserved failure record cannot stand in
for a later live result.

## Accepted live replay on 2026-09-29

`OPS_DEEPFLOW_ORBSTACK_POC=1 go test ./test/e2e -run '^TestDeepFlowPOC$' -count=1 -v -timeout=30m`
exited 0 in 724.50 seconds. The raw log is
`artifacts/test-reports/task-2.5-live-uid-runtime-selector-20260929.log`;
post-run source/manifest/Fixture hashes are recorded in
`task-2.5-live-evidence-binding-20260929.json`.

The policy-only kube-router prerequisite from the core offline environment
actually programs scoped deny rules. The test checks those rules before
approving each runtime Pod UID through a ConfigMap-backed native-image init
gate. A replacement Agent UID must be approved after its own rules are
programmed. The test then checks the current four runtime Pod namespaces:
internal DNS and MySQL connect; raw TCP attempts to 1.1.1.1 and 8.8.8.8 on
80/443 and 2606:4700:4700::1111 on 80/443 fail. The standalone probe has the
same release-scoped policy and also rejects public TCP. API exceptions are
limited to the selected cluster's observed private backend/VIP and ports,
without a node-wide or public CIDR allow rule.

The actual upstream default Agent group is configured using its REST API and
the observed private Server Pod IP with internal controller/ingester ports.
After upstream registration reports state 1/RUNNING for this PoC cluster,
the test creates 24 real MySQL Service connections and queries `/v1/query/`
for `ip_0`, `ip_1`, `server_port`, `flow_id`, `retrans_tx` and `retrans_rx`.
One real matching row was observed; both retransmission counters were zero.
The redacted live Fixture retains port, counters and observed-field presence;
it does not synthesize endpoint IDs or claim induced packet loss.

```yaml
networkEvidence: true
networkEvidenceMode: live
l7Context: false
traceCompletion: false
```

Earlier retries and their original errors remain in the `task-2.5-live-*`
logs: unavailable Agent/API connectivity, stdout contamination by a kubectl
warning, the `simple_sql` bypass selecting the wrong table, a fresh probe
starting before policy convergence, and the query-stage selector counting the
standalone probe as a runtime Pod. The final test uses clean stdout, the
standard Querier path, per-UID gates and runtime component selectors; none of
those earlier failures is represented as a pass.

Cleanup removed only release-labeled PoC resources, including its Lease;
afterward the release selector returned no resources. Core, existing Victoria
and external OpenBao remained deployed. The unchanged v7.2.0 digests and
runtime dependency list above remain the exact tested inputs. DeepFlow stays
candidate outside the core Bundle until its separate full distribution
license/source admission is completed.
