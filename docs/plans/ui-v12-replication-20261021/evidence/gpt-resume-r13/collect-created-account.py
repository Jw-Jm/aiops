from pathlib import Path
import json
import subprocess
from uuid import UUID

r = Path('/data/ops-ui-v12-mfa-20261022-r1/gpt-resume-r13')
user = str(UUID(json.loads((r / 'preview/private/browser-created-account.json').read_text())['userId']))
expected = json.loads((r / 'preview/logs/users-browser-result.json').read_text())['account']['subject']
query = f"""SELECT json_build_object('userId',a.user_id,'tenantId',a.tenant_id,'subject',a.subject,
'credentialVersion',a.credential_version,'mustChangePassword',a.must_change_password,'status',a.status,
'currentGrants',(SELECT count(*) FROM identity.role_grants g WHERE g.tenant_id=a.tenant_id AND g.user_id=a.user_id AND g.status='active' AND g.authorization_version=a.authorization_version),
'passwordAudit',(SELECT json_agg(json_build_object('recordId',record_id,'subject',subject,'event',event_type,'time',created_at)) FROM audit.records WHERE tenant_id=a.tenant_id AND entity_id=a.user_id AND event_type='identity.native_password.changed'))
FROM identity.accounts a WHERE a.user_id='{user}';"""
raw = subprocess.check_output(['docker', 'exec', 'ops-ui-v12-mfa-pg-20261022', 'psql', '-U', 'postgres', '-d', 'ui_v12_native_identity', '-X', '-tAc', query], text=True)
data = json.loads(raw)
assert data['subject'] == expected and data['credentialVersion'] == 2
assert not data['mustChangePassword'] and data['currentGrants'] == 0
assert len(data['passwordAudit']) == 1 and data['passwordAudit'][0]['subject'] == expected
(r / 'created-account-persistence.json').write_text(json.dumps(data, indent=2))
print('created account password version, mandatory-change completion, zero grants and actual audit principal verified')
