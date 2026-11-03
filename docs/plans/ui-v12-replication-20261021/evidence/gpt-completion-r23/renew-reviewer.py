"""Renew only the expired task TokenReview credential; preserve trust and roles."""
import base64
import datetime
import http.client
import json
from pathlib import Path
import socket
import ssl
import subprocess

root = Path('/data/ops-v12-completion-20261026-r1')
bao = Path('/data/ops-ui-v12-mfa-20261022-r1/gpt-r6-openbao')
context = ssl.create_default_context(cafile=str(bao / 'tls/ca.crt'))
token = json.loads((bao / 'private/initialization.json').read_text())['root_token']

class Connection(http.client.HTTPSConnection):
    def connect(self):
        self.sock = context.wrap_socket(socket.create_connection(('127.0.0.1', 18206), 5), server_hostname='mfa-gpt-r6.ops.local')

def request(path, data=None):
    conn = Connection('127.0.0.1', 18206)
    conn.request('GET' if data is None else 'POST', '/v1/' + path,
                 body=None if data is None else json.dumps(data),
                 headers={'Content-Type': 'application/json', 'X-Vault-Token': token})
    response = conn.getresponse()
    raw = response.read()
    if response.status not in (200, 204):
        raise RuntimeError('OpenBao credential maintenance failed: HTTP ' + str(response.status))
    return json.loads(raw) if raw else {}

role = request('auth/kubernetes/role/ops-api-workload')['data']
assert role['bound_service_account_names'] == ['ops-api']
assert role['bound_service_account_namespaces'] == ['ops-ui-v12-mfa-20261022-gpt']
assert role['audience'] == 'openbao' and role['token_policies'] == ['ui-v12-mfa']
before = request('auth/kubernetes/config')['data']
assert before['kubernetes_host'] == 'https://192.168.0.63:6443'
assert before['disable_local_ca_jwt'] is True and before['disable_iss_validation'] is False
backup = root / 'private/bao-auth-config-before.json'
backup.write_text(json.dumps(before)); backup.chmod(0o600)
raw = subprocess.check_output(['kubectl', '-n', 'ops-ui-v12-mfa-20261022-gpt', 'create', 'token', 'bao-reviewer', '--duration=1h'], text=True).strip()
claims = json.loads(base64.urlsafe_b64decode(raw.split('.')[1] + '=='))
assert claims['sub'] == 'system:serviceaccount:ops-ui-v12-mfa-20261022-gpt:bao-reviewer'
assert claims['aud'] == ['https://kubernetes.default.svc.cluster.local']
path = root / 'private/reviewer.jwt'; path.write_text(raw + '\n'); path.chmod(0o600)
keys = ('kubernetes_host', 'kubernetes_ca_cert', 'disable_local_ca_jwt', 'disable_iss_validation', 'issuer', 'pem_keys')
config = {key: before[key] for key in keys if key in before}
request('auth/kubernetes/config', {**config, 'token_reviewer_jwt': raw})
after = request('auth/kubernetes/config')['data']
assert all(after[key] == value for key, value in config.items())
assert request('auth/kubernetes/role/ops-api-workload')['data'] == role
result = {'reviewer': claims['sub'], 'expiresAt': datetime.datetime.fromtimestamp(claims['exp'], datetime.timezone.utc).isoformat(),
          'changed': 'task TokenReview JWT only', 'roleUnchanged': True, 'trustConfigUnchanged': True,
          'noKeyOrPolicyChange': True, 'scope': 'temporary task credential; no automatic projection rotation claimed'}
(root / 'evidence/reviewer-renewal.json').write_text(json.dumps(result, indent=2) + '\n')
print(json.dumps(result))
