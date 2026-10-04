# SP06 locked HolmesGPT runtime reuse PoC

This admission applies to HolmesGPT 0.42.0 at
bfd33247f154484bc2f734a0da709da133c6a434, unmodified Apache-2.0 source, with
Python MCP 1.28.1 and Go MCP SDK v1.1.0 (2025-06-18 negotiation).
`docs/evidence/sp06-20261003/upstream-installed-source.json` compares the
installed package with the exact tag. The release-only version stamp is
recorded explicitly. The wrapper imports DefaultLLM, ToolCallingLLM and the
upstream executor; official provider/tool extension points surround calls with
platform admissions and translate contracts. No core loop, provider client or
upstream prompt is copied. Built-in production/source/write tools are absent
from the admitted executor. The investigator has no database or source secrets.

Fresh real service evidence is `real-chain-r16.log` (exit 0): actual Keycloak
Bearer API -> durable Go Job/Dispatcher -> resident Python investigator ->
Ollama llama3.1:8b-16k -> official SDK Streamable HTTP handshake/tool call ->
existing Incident semantics -> fenced Ledger/audit -> final Go validation ->
persisted SSE including API restart/resume. `concurrent-real-r2.log` records ten
concurrent actual investigations with the main Finding/Incident reads kept
available; honest model timeouts are terminal failures, never reported as
successful diagnoses. `process-recovery-r2.log` records actual SIGKILL/lease
expiry/takeover and no duplicate committed success. Provider error fixtures
supplement these real model gates; they do not replace them. This small local
model is a protocol/budget/tool-loop gate, not an RCA accuracy model.

`investigator-offline-build-r4.log` (exit 0) builds the pinned Linux/arm64 image
with network disabled and no pull, hash-authenticates all 175 prepared wheels,
checks installed dependencies and imports the real provider/core/MCP wrapper.
`wheel-offline-rebuild-r2.log` reproduces the clickhouse-sqlalchemy source-built wheel without
network and compares every ZIP payload member. Tiktoken and google-crc32c
likewise use unmodified Python publisher sources plus explicit platform-native
locks; independent offline rebuilds compare all payloads. The Rust source
inventory includes 767 publisher-declared crate versions, with conditional
build/test/target dependencies explicitly distinguished from runtime linkage.
Measured C extensions and their exact native build sources/notices are also
included; no ambiguous publisher CRC wheel linkage is claimed. `base-source-r3.log` preserves
87 Debian binary packages, 61 exact source packages and signed publisher
indexes. Corresponding source and original notices include the Python, base,
CPython/pip, build tool and GPL wrapper closure. Catalog image/source/notice
hashes and finite native-license records bind this exact material; missing or
changed source is rejected by Bundle admission. See ADR0027 and
`third_party/admission/sp06-investigator-license-review.json` for the precise
licenses. The whole closure is not relabeled Apache-2.0.

Full SP06 signed native Chart/Bundle, final regression and complete independent
review remain mandatory separate delivery gates. Their current status is in
`docs/evidence/sp06-20261003/task-ledger.json`; this reuse PoC does not declare
SP06 complete. PyRCA is excluded. VM/KubeVirt/CDI remain disabled/unverified.
Performance, capacity and P95 have a user waiver and are not passed.
