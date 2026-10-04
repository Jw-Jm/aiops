# ADR-0026: SP-06 nonvirtual investigation authorization

Status: Accepted scope; implementation and runtime acceptance pending
Date: 2026-10-03

The user authorizes all Tasks 6.1–6.6 and §14.6, superseding older stage
restrictions in AGENTS.md and ADR0019. Root specifications 00–10 remain
unchanged. ADR0008 continues to defer KubeVirt/CDI and VM tools; they remain
disabled/unverified. ADR0020 keeps PyRCA disabled and excluded from every
runtime image, including investigator.

The only newly authorized resident runtime is the specified HolmesGPT
investigator. Reuse its exact admitted source, provider and investigation loop;
platform wrappers translate contracts and apply official extension points.
The Platform MCP endpoint belongs to platform-api. No command/SSH/exec/SQL/
arbitrary URL tool or execution handle is exposed. SP07–09 remain outside scope.

Dedicated performance, sustained load, capacity and P95 measurements are waived
by the user, not passed. Correctness deadlines, ten concurrent investigations
with the main chain, security, budgets, real model/MCP, process recovery and
affected offline material gates are mandatory.

All current execution evidence and final complete independent read-only review
belong to docs/evidence/sp06-20261003. Historical reports establish context only.
Initial source is main@1c748b5b1f0bf33fb54774c2068a735abe2be599 with no user
modifications; origin/main matched after fetch. Work occurs on
sp06/nonvirtual-investigation. Shared services and their data are preserved.

No PASS or final merge/push until all authorized tasks, mandatory actual gates,
confirmed fixes and final independent full-delivery PASS are complete. New
mandatory context semantics use a new payload version and forward-only schema
migrations; no historical schema or failure evidence is rewritten.
