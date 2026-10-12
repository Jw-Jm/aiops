# Native amd64 Keycloak material admission

The authenticated Keycloak 26.7.4 native distribution retains exact measured package name/version parity with the previously qualified publisher source material. All 574 source inventory files and 45 catalog notice digests were verified independently.

The isolated native probe reproduced the deployment chart's prepare/build step, then ran the optimized server with a PostgreSQL backend and a read-only root filesystem. Master-realm OIDC discovery, JWKS and management readiness passed. No public network was available to these services. [Native proof](../evidence/amd64-deployment-20261009/keycloak-native-poc-result.json) and [source integrity](../evidence/amd64-deployment-20261009/keycloak-shared-source-integrity.json).

First-start probe failures remain preserved. This admission concerns native distribution and startup capabilities; release TLS, tenant identity bootstrap and platform authorization remain separate acceptance work.
