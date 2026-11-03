"""Owned development namespace only; retain every failed stage and private secret."""
import json
import os
from pathlib import Path
import socket
import subprocess
import yaml

assert socket.gethostname() == 'mssc'
root = Path('/data/ops-v12-native-cold-r34-20261027')
assert root.is_dir() and root.stat().st_uid == os.getuid()
private = root / 'private'
private.mkdir(mode=0o700)
(root / 'evidence').mkdir()
namespace = 'ops-native-v12-r34-20261027'

def run(args, **kwargs):
    result = subprocess.run(args, capture_output=True, text=True, **kwargs)
    if result.returncode:
        # Commands contain paths, never credentials. Raw failure is retained privately.
        name = 'failure-' + str(len(list(private.glob('failure-*')))) + '.log'
        (private / name).write_text(result.stdout + result.stderr)
        (private / name).chmod(0o600)
        raise RuntimeError(f'command failed: {args[0]}; private log={name}')
    return result.stdout

def create(document):
    return run(['kubectl', 'create', '-f', '-'], input=json.dumps(document))

create({'apiVersion': 'v1', 'kind': 'Namespace', 'metadata': {'name': namespace, 'labels': {'ops.local/task': 'ui-v12-native-cold-r34'}}})
profile = yaml.safe_load(Path('/data/ops-v12-native-visual-r24-20261026/resolved-profile.yaml').read_text())
profile['components']['keycloak'] = {'mode': 'disabled'}
profile['components']['openbao']['endpoint'] = f'https://ops-openbao.{namespace}.svc.cluster.local:8200'
profile['components']['openbao']['namespace'] = namespace
(root / 'resolved-profile.yaml').write_text(yaml.safe_dump(profile, allow_unicode=True))
values = {'components': {'openbao': {'image': profile['components']['openbao']['image']}}}
(root / 'bao-values.json').write_text(json.dumps(values))
raw = run(['helm', 'template', 'ops-r34', str(root / 'source/deploy/charts/ops-dependencies'), '-n', namespace, '-f', str(root / 'bao-values.json'), '-s', 'templates/openbao.yaml'])
data = root / 'bao-data'
data.mkdir(); os.chown(data, 100, 1000); data.chmod(0o770)
documents = [d for d in yaml.safe_load_all(raw) if d]
for document in documents:
    if document['kind'] == 'StatefulSet':
        document['spec'].pop('volumeClaimTemplates')
        spec = document['spec']['template']['spec']
        spec['automountServiceAccountToken'] = False
        spec['volumes'].extend([
            {'name': 'data', 'hostPath': {'path': str(data), 'type': 'Directory'}},
            {'name': 'reviewer', 'projected': {'defaultMode': 0o444, 'sources': [
                {'serviceAccountToken': {'path': 'token', 'expirationSeconds': 600}},
                {'configMap': {'name': 'kube-root-ca.crt', 'items': [{'key': 'ca.crt', 'path': 'ca.crt'}]}},
                {'downwardAPI': {'items': [{'path': 'namespace', 'fieldRef': {'fieldPath': 'metadata.namespace'}}]}}
            ]}}
        ])
        spec['containers'][0]['volumeMounts'].append({'name': 'reviewer', 'mountPath': '/var/run/secrets/kubernetes.io/serviceaccount', 'readOnly': True})
    if document['kind'] != 'ClusterRoleBinding':
        document['metadata']['namespace'] = namespace
    create(document)
(root / 'bao-canary.yaml').write_text('\n---\n'.join(yaml.safe_dump(d) for d in documents))
cli = str(root / 'bin/opsctl')
common = ['--profile', str(root / 'resolved-profile.yaml'), '--ca-file', str(private / 'bao-ca.pem'), '--recovery-file', str(private / 'bao-recovery.json')]
for index, (operation, extra) in enumerate([('init', []), ('unseal', ['--share-index', '0']), ('unseal', ['--share-index', '1'])]):
    output = run([cli, 'openbao', operation] + common + extra, cwd=root / 'source')
    (root / 'evidence' / f'bao-{index}-{operation}.log').write_text(output)
print(json.dumps({'namespace': namespace, 'stage': 'isolated OpenBao initialized/unsealed', 'projectedReviewerTTL': 600, 'storage': 'new task hostPath; not shared PVC or HA acceptance'}))
