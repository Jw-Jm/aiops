# Native amd64 OpenBao material admission

OpenBao 2.7.0 was fetched using its immutable official multi-architecture index, selecting only the authenticated Linux amd64 manifest/config/layer closure. The native image contains the same 320 measured package name/version identities as the retained admitted distribution.

The complete corresponding source archive and all 662 inventory members were independently hash verified, together with 267 original catalog notices. Native dependency parity introduces no additional source identities. The separate amd64 license registry binds existing publisher source and notice bytes to the measured native image index; historical ARM scopes remain unchanged.

The isolated native PoC initialized and unsealed a single-node Raft backend and passed KV v2 write/read/delete. It ran with network disabled and a read-only root filesystem. [PoC evidence](../evidence/amd64-deployment-20261009/openbao-native-poc-result.json), [source integrity](../evidence/amd64-deployment-20261009/openbao-shared-source-integrity.json), [dependency parity](../evidence/amd64-deployment-20261009/openbao-dependency-parity.json).

This qualifies component distribution material. Formal deployment TLS, PKI, bootstrap and platform capabilities still require installed release acceptance; the isolated loopback PoC does not establish those properties.
