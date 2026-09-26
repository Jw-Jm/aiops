# Third-party source snapshots

No upstream source snapshot has passed the version, digest, file-level license,
dependency-closure, PoC, and rollback gates yet. `sources: []` is intentional.

Only sources selected for a direct vendor or fork may be placed under
`third_party/sources/`, and each entry in `manifest.yaml` must identify the
upstream URL and commit, selected-source SHA-256, included paths, patch digests,
file-level licenses, transitive dependency closure, owner, conformance fixtures,
update policy, and rollback plan. Candidate catalog entries are not Bundle
inputs and do not authorize copying upstream files.
