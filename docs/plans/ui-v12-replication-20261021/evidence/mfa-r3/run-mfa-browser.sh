#!/bin/bash
# Prepares one fresh native administrator on the task-owned instance and runs
# the browser acceptance against it. The identity is independent from the
# delivered v12.admin account and from every other round.
set -eu
BASE=/data/ops-ui-v12-mfa-20261022-r1
cd "$BASE/preview"
login_name="mfa.admin.$(openssl rand -hex 5)"
password=$(openssl rand -hex 18)
tenant_id=$(python3 -c "import uuid;print(uuid.uuid4())")
slug="mfa-preview-$(echo "$tenant_id" | cut -c1-8)"
printf "%s" "$password" > private/browser-identity-password
chmod 600 private/browser-identity-password
cat > private/bootstrap-admin.input.json <<JSON
{"databaseUrl":"$(cat private/bootstrap.dsn)","tenantId":"$tenant_id","loginName":"$login_name","displayName":"MFA Browser Administrator"}
JSON
chmod 600 private/bootstrap-admin.input.json
docker exec -i ops-ui-v12-mfa-pg-20261022 psql -U postgres -d ui_v12_native_identity -v ON_ERROR_STOP=1 -q <<SQL
INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES('$tenant_id','$slug','MFA Browser Tenant');
SQL
./opsctl bootstrap native-administrator --input private/bootstrap-admin.input.json --password-file private/browser-identity-password >/dev/null
python3 - "$login_name" "$password" <<'PY'
import json, sys
with open('/data/ops-ui-v12-mfa-20261022-r1/preview/private/browser-identity.json', 'w') as handle:
    json.dump({'loginName': sys.argv[1], 'password': sys.argv[2]}, handle)
PY
chmod 600 private/browser-identity.json
# The entry rate-limit fence is task owned; a fresh run starts from a clean
# counter so the 429 check measures the real threshold.
docker exec -i ops-ui-v12-mfa-pg-20261022 psql -U postgres -d ui_v12_native_identity -v ON_ERROR_STOP=1 -q \
  -c "DELETE FROM identity.login_entry_attempts" >/dev/null
# The API serves one fixed tenant, so the run needs the API to point at the
# freshly created tenant.
printf "%s" "$tenant_id" > private/tenant-id
systemctl restart ops-ui-v12-mfa-api
sleep 3
/opt/ops-assistant-browser-20261014/node mfa-browser.mjs
