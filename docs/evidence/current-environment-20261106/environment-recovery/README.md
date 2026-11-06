# Environment recovery follow-up

This record covers the 2026-11-06 metrics APIService migration, scheduler/route/CNI triage, locked amd64 Analyzer rerun, signed Bundle decision, and `/dev/sda1` storage triage.

## Result

- `v1beta1.metrics.k8s.io` is served by `ops-current/ops-metrics-server-metrics`, reports `Available=True`, and returns real node samples. The old backend is scaled to zero for rollback. A one-off namespace was deleted naturally and is now `NotFound`.
- The scheduler now reaches the API server through `127.0.0.1:6443` with TLS server name `mssc`; its former Wi-Fi/network failure path is removed. Node routing, flannel, `cni0`, CoreDNS endpoints, and `readyz` pass.
- The locked amd64 K8sGPT live gate passes in 24.26 seconds with the admitted `e6ce7073…` binary.
- Historical Bundle r11 verifies completely, but neither r11 nor damaged r12 matches current `b3d7208b…`; installing one would roll back current, so no installation was performed.
- `/dev/sda1` is a Seagate ST2000DM008 with poor random-sync latency (8.93 IOPS; p95 185.5 ms). The USB/exFAT `sdb3` benchmark is faster on average but has 0.2–1.4 second long tails and no POSIX/data-integrity semantics for the live stack.

## Boundary

The scheduler network mitigation does not claim that all control-plane restart causes are fixed. At 04:05Z, API-server, scheduler, controller-manager, metrics-server, and API probes timed out together and the API-server container exited 137 after about four hours. This is consistent with the retained shared-storage latency hypothesis and remains a live environment risk.

Do not place live databases, Bao, SeaweedFS, Victoria data, or container runtime state on the existing exFAT USB volume. Destructive migration requires explicit approval, a verified backup of the existing 45 GiB on `sdb3`, reformat to a Linux filesystem, sustained direct-I/O and power-loss tests, and a service-by-service rollback plan.
