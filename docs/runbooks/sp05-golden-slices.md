# SP05 nonvirtual golden fault replay

Use the repository-locked toolchain. Prepare the current PostgreSQL, OIDC,
OpenBao Transit and TLS/IAM S3 dependencies before running the live entries.
Do not source or print credentials in a report. The integration test owns its
isolated database, tenant, signed Recipe keys and object-store Fixture and
cleans only identities created by that invocation.

Run from the platform repository:

```sh
make check-toolchain
OPS_PERFORMANCE_EXEMPTION=sp05-user-20261002 \
  go test ./test/integration \
  -run TestSP05FrozenNonvirtualGoldenVerticalAndReplay -count=1 -v
```

This required entry fails on missing dependencies. It authenticates each frozen v3/v4
manifest SHA-256 before reading native snapshots/routes/log rows and independently
frozen expectations. Database receivedAt is the actual service clock. Only
trusted Go constructors receive the frozen input clock; HTTP/runtime config
cannot override it. Expect two semantic replays per scenario/variant with
unchanged Finding/Incident/Evidence identities and RCA revision.

## DIMM failure

`test/fixtures/incidents/dimm-failure/v3` supplies native Redfish inventory,
current MemoryMetrics uncorrectable alarm and DIMM health. The actual locked
Gofish decoder produces hardware identities and Candidates. Expected causal
path is DIMM -> PhysicalServer -> hosted Node -> Pod. Current health and ECC are
independent required facts. Lifetime ECC counters alone cannot confirm a fault.
Missing metrics, healthy conflicting observation or required source degradation
must return unresolved/partial. These protocol inputs are not physical BMC
acceptance; production hardware Fixture mode remains degraded/time-unreliable.

## PVC/CSI failure

`test/fixtures/incidents/pvc-csi-failure/v3` supplies native PVC, Pod, Event,
StorageClass and CSIDriver. The official Inspector passes FindingCandidate to
unified ingestion. A generic ProvisioningFailed event is insufficient: its
native reportingController must match the SC -> CSIDriver identity. Impact
contains the consuming Pod; Event observations remain diagnostic evidence and
are not affected resources. Missing Event proof, a later Bound PVC or source
unavailability must remain unresolved/partial.

## Node failure

`test/fixtures/incidents/node-failure/v4` supplies native Ready=False and an
original KernelOops line. The VictoriaLogs adapter queries exact trusted tenant,
cluster, Node UID and empty namespace labels. Its positive canary and negative
caller expansions establish current source isolation before NPD consumption.
The unchanged locked NPD matcher produces Candidates via common ingestion.
Missing log evidence, later Ready=True or degraded required sources must remain
unresolved/partial. Physical Node failure injection is not claimed.

Node Recipe v2 additionally gates a located fatal DIMM causal record; its
publication/activation and causal counterexamples have separate regression
entries. Do not mix this alternative's evidence into the v3 frozen kernel-fault
expectations or rewrite a manifest from business output.

All referenced RCA Evidence must be readable with digest verification, protected
by the SP04 recursive dependency closure for at least 365 days, and reconciled
to actual object-store compliance protection. On any failure preserve the raw
sanitized log, source version and exit code, fix the cause and rerun affected
entries. Do not change frozen expectations or loosen correctness assertions.
VM launch/network scenarios remain deferred and unverified under ADR0008.
Performance-specific gates are user-waived and are not counted as PASS.
