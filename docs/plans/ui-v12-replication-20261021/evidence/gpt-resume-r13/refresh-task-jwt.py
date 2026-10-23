"""Refresh existing task credentials without creating mounts, keys or identities."""
import base64
import json
import os
from pathlib import Path
import ssl
import subprocess
import time
import urllib.error
import urllib.request

task = Path('/data/ops-ui-v12-mfa-20261022-r1')
bao = task / 'gpt-r6-openbao'
ns = 'ops-ui-v12-mfa-20261022-gpt'
label = 'ui-v12-mfa-gpt-20261022'
ctx = ssl.create_default_context(cafile=str(bao / 'tls/ca.crt'))

def command(args):
    p = subprocess.run(args, text=True, capture_output=True)
    if p.returncode:
        raise RuntimeError('task Kubernetes command failed')
    return p.stdout.strip()

def req(path, body=None, token=None):
    headers = {'Content-Type': 'application/json'}
    if token:
        headers['X-Vault-Token'] = token
    request = urllib.request.Request('https://127.0.0.1:18206/v1/' + path,
        data=json.dumps(body).encode() if body is not None else None, headers=headers)
    with urllib.request.urlopen(request, context=ctx, timeout=10) as response:
        raw = response.read()
        return json.loads(raw) if raw else {}

assert command(['kubectl', 'config', 'view', '--minify', '-o', 'jsonpath={.clusters[0].cluster.server}']) == 'https://192.168.0.63:6443'
for kind, name in [('namespace', ns), ('clusterrole', 'ops-ui-v12-mfa-20261022-tokenreview'), ('clusterrolebinding', 'ops-ui-v12-mfa-20261022-tokenreview')]:
    obj = json.loads(command(['kubectl', 'get', kind, name, '-o', 'json']))
    assert obj['metadata']['labels']['ops.local/task'] == label
assert req('sys/init')['initialized'], 'existing Bao must already be initialized'
assert not req('sys/seal-status')['sealed'], 'existing Bao must be unsealed; inspect before proceeding'
root_token = json.loads((bao / 'private/initialization.json').read_text())['root_token']
role = req('auth/kubernetes/role/ops-api-workload', token=root_token)['data']
assert role['bound_service_account_names'] == ['ops-api']
assert role['bound_service_account_namespaces'] == [ns] and role['audience'] == 'openbao'
assert role['token_policies'] == ['ui-v12-mfa']
policy = req('sys/policies/acl/ui-v12-mfa', token=root_token)['data']['policy']
expected_policy = 'path "transit/encrypt/ui-v12-mfa" { capabilities=["update"] }\npath "transit/decrypt/ui-v12-mfa" { capabilities=["update"] }'
assert policy == expected_policy, 'unexpected policy; do not overwrite'
key = req('transit/keys/ui-v12-mfa', token=root_token)['data']
assert not key['exportable'] and not key['allow_plaintext_backup']
old_config = req('auth/kubernetes/config', token=root_token)['data']
assert old_config['kubernetes_host'] == 'https://192.168.0.63:6443'
tokens = {}
for sa, filename, audience in [('bao-reviewer', 'reviewer.jwt', None), ('ops-api', 'projected.jwt', 'openbao'), ('wrong-workload', 'wrong.jwt', 'openbao')]:
    obj = json.loads(command(['kubectl', '-n', ns, 'get', 'serviceaccount', sa, '-o', 'json']))
    assert obj['metadata']['labels']['ops.local/task'] == label
    args = ['kubectl', '-n', ns, 'create', 'token', sa, '--duration=1h']
    if audience:
        args += ['--audience=' + audience]
    tokens[filename] = command(args)
    tmp = bao / 'private' / (filename + '.refresh')
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as f:
        f.write(tokens[filename])
    os.replace(tmp, bao / 'private' / filename)
claims = json.loads(base64.urlsafe_b64decode(tokens['projected.jwt'].split('.')[1] + '==='))
assert claims['exp'] > time.time() + 3000
assert old_config['issuer'] == claims['iss']
req('auth/kubernetes/config', {
    'kubernetes_host': old_config['kubernetes_host'],
    'kubernetes_ca_cert': (bao / 'tls/kubernetes-ca.crt').read_text(),
    'token_reviewer_jwt': tokens['reviewer.jwt'],
    'disable_local_ca_jwt': old_config['disable_local_ca_jwt'],
    'disable_iss_validation': old_config['disable_iss_validation'], 'issuer': old_config['issuer'],
}, root_token)
login = req('auth/kubernetes/login', {'role': 'ops-api-workload', 'jwt': tokens['projected.jwt']})
assert login['auth']['lease_duration'] <= 900
try:
    req('auth/kubernetes/login', {'role': 'ops-api-workload', 'jwt': tokens['wrong.jwt']})
    raise AssertionError('wrong workload accepted')
except urllib.error.HTTPError as e:
    assert e.code == 403
result = {'status': 'passed', 'namespace': ns, 'checks': ['existing initialized/unsealed TLS/Raft instance', 'existing exact task role/policy/key verified', 'task reviewer and workload JWT refreshed', 'real TokenReview login accepted; wrong serviceaccount rejected'], 'expiresAtEpoch': claims['exp'], 'limitation': 'one-hour test credentials; deployment rotation not verified'}
out = task / 'gpt-resume-r13/bao-refresh-result.json'
with out.open('x') as f:
    json.dump(result, f, indent=2)
print('existing task Bao JWT refreshed and authentication verified; no keys or mounts created')
