# ADR-0018: SP-04 nonvirtual development authorization

Status: Accepted (scope); implementation acceptance pending

On 2026-10-01 the user authorized SP-04 Tasks 4.1–4.7, superseding the
SP-02-only stage text in AGENTS.md. ADR-0008 remains effective. Root documents
00–10 are unchanged. SP-05–SP-09 business flows remain outside this change.

Implement the current specification, including 10 §14.2–14.4, 05 §25,
07 §30 and 08 §49–51. Historical PoCs do not qualify the current runtime.
Keep the original reuse locks as historical baselines; any production admission
requires an explicit additive source/patch/closure record and current replay.
Do not run an alternative graph kernel when upstream adaptation fails.

Public mandatory payload changes require new payload schema versions. Internal
Graph HTTP is part of platform-worker, authenticated with mTLS and a short-lived
audience-bound user scope; PostgreSQL routing never elects an owner.

Acceptance and independent review records live in docs/evidence/sp04-20261001/.
No deployment to the existing ops-system release or cleanup of its data is
authorized by a test fixture. Tests own independently identifiable resources.
