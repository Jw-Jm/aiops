# Linux/amd64 shared Go SDK source admission

The same exact OPA v1.21.0 and 99 locked runtime module identities are retained. Publisher module archives, original notices, subtree license decisions and selected upstream source identities are verified by `scripts/prepare-runtime-source.py` for the actual amd64 import closure. The independently retained target source includes 3,049 module files and 1,407 standard library files; no arm64 source archive is relabeled.

Reviewed corresponding source: `sha256:f9530a88b9da0d640c4fe5b40def0805ae79ea958ea6f460cf78a0402937b33d`. The retained-source offline build of API, Worker, opsctl and command-runner passed with GOPROXY/GOSUMDB off and an initially empty module cache; the compiler build cache was retained. The reviewed archive differs only in the lock metadata from the archive actually rebuilt, with every code and notice byte compared.

All 12 policy tests executed successfully on 192.168.0.63 Linux/amd64, including scoped decisions, denial, signed policy/compiler restrictions and acknowledgement requirements. Proofs: docs/evidence/amd64-deployment-20261009/runtime-offline-rebuild.json and policy-native-test.log. This qualifies the exact source/SDK architecture closure; it does not report complete platform authorization, deployment, or SP01-SP07 business acceptance.
