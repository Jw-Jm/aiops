# Keycloak bootstrap

This realm is imported into Keycloak 26.7.4 as the platform OIDC issuer. Keep the bootstrap administrator password in the deployment Secret; never add it to this repository, a rendered bundle, or command output.

## Realm import

1. Create a one-time `ops-keycloak-auth` Secret with a unique bootstrap administrator password and deploy the pinned Keycloak image from the dependency chart.
2. Import [`realm-ops.json`](../../deploy/keycloak/realm-ops.json) at first startup. Before production login, replace the example `platform.example.com` callback and web origin with the exact HTTPS URLs used by the platform.
3. Confirm the realm issuer is `https://<keycloak-host>/realms/ops`, the `ops-web` client uses Authorization Code with S256 PKCE, and the `ops-api` client is used only as an access-token audience.
4. The imported `ops-browser` flow supports Level 1 username/password and Level 2 OTP. It maps `urn:ops:loa:1` to LoA 1 with max age 36000 seconds and `urn:ops:loa:2` to LoA 2 with max age 0. Verify these bindings after import and review a realm export before promoting changes.
5. Assign each subject's `tenant_id` and `tenant_ids` attributes through the platform's approved tenant assignment process. The realm user profile makes these attributes administrator-only. The API checks the signed membership claim and independently resolves active tenant role bindings and scopes from PostgreSQL.

## Bootstrap administrator retirement

After the first named realm administrator is provisioned and verified, rotate the bootstrap password in the Secret store, disable or remove the temporary bootstrap principal, restart Keycloak, and verify that the named administrator can manage the `ops` realm. Do not keep the initial bootstrap credential as an operating credential.

## Verification

- Confirm OIDC discovery and JWKS retrieval over HTTPS.
- Complete a browser Authorization Code login and verify the exact callback URI, state, nonce, and S256 code challenge.
- Confirm access tokens carry the `ops-api` audience and signed tenant membership claims.
- Request `urn:ops:loa:2` with `max_age=0` and `prompt=login`; verify the returned ID token has the requested ACR, `auth_time`, and Keycloak session ID. Reject a response with a lower ACR.
- Persist the returned step-up identity in `platform.step_up_sessions` and verify that an expired or differently bound session cannot authorize a high-privilege request.
- Keep the Keycloak test realm and its database isolated from the installed core Keycloak service.
