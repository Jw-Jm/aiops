# ADR-0013: Lock the licensed K8sGPT CLI baseline

Status: Accepted

## Decision

For Task 2.9's read-only, no-LLM Analyzer JSON contract, select upstream
K8sGPT `v0.3.41`, commit `f071b32aa85d77b197cd2d0f9868294f7b55c5eb`.
Use the unchanged upstream CLI and its Pod Analyzer. No Analyzer fork,
`--explain`, provider invocation, mutation or automatic remediation is selected.

The accepted arm64 CLI production import closure contains 233 exact Go module
artifacts. The three generated K8sGPT SDKs use frozen Schema commit
`327bb733aec02ff9ed8af18cdb212449e8c2b263`, byte-matched against Buf commit
`7a91c862051546c892f89bdd97f5d31a`. The lock includes Schema, generator,
applicable module and nested license/notice files. MPL-covered dependency
sources remain available as exact module artifacts; their license and source
obligations apply to any future redistribution or modification.

The source/Fixture qualification is limited to Task 2.9. The Component Catalog
entry remains candidate and cannot enter a Bundle. Platform runtime integration
and target-specific operational compatibility require their later task gates.

## Rationale and evidence

Task 2.9 chooses an exact upstream baseline; the formal design does not fix a
K8sGPT release. The frozen `v0.4.36` CLI closure imports two Interplex generated
SDKs whose Schema license could not be established. It is not selected; its
rejected license audit and closure remain recorded. Generator licensing and
public repository visibility cannot substitute for Schema rights.

The selected release contains no Interplex dependency. Its upstream Pod tests,
arm64 build, repeated deterministic JSON output and exact production import
closure are tested in digest-locked containers with network disabled and no
image pulls or dependency downloads. `inspection-reuse-lock.yaml` pins source,
license evidence, fixtures, mappings and actual replay logs.

## Alternatives

- Keep the Interplex-dependent release disabled pending rights-holder evidence:
  safe, but it does not close this capability's current license gate.
- Assign the generator or K8sGPT root license to Interplex: rejected because the
  frozen Schema does not grant those rights.
- Create an Analyzer fork: unnecessary while the unchanged upstream CLI meets
  the required JSON behavior and has a reviewable dependency closure.

## Consequences

Future consumers use this exact CLI/JSON baseline and the locked platform
projection. The fixture proves the selected Pod behavior; it is not broad
Kubernetes production compatibility, an LLM result or live hardware evidence.
All other selected inspection, incident and hardware surfaces retain their
existing restricted source/model/Fixture boundaries.

## Rollback conditions

Source, generated Schema, artifact, license or import-closure drift, a failed
no-network replay, non-deterministic JSON or a provider/network call disables
this capability pending review. Do not switch releases automatically, loosen
license checks, or replace the selected upstream behavior with a custom
framework. KubeVirt/CDI remain deferred under ADR-0008.
