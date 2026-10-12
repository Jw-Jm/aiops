#!/usr/bin/env python3
"""Remove only an owned, failed Metrics addon that has never started a Pod.

Requires explicit formal namespace identity. Preserves bootstrap TLS/CA objects,
namespace, storage and other releases. Run only after the installer has exited.
"""
import argparse
import json
import subprocess
import yaml

parser = argparse.ArgumentParser(description=__doc__)
for flag in ('context', 'namespace', 'namespace-uid', 'installation-id'):
    parser.add_argument('--' + flag, required=True)
args = parser.parse_args()
release = 'ops-metrics-server'
name = release + '-metrics'

def kubectl(*words):
    return json.loads(subprocess.check_output(['kubectl', '--context', args.context, *words, '-o', 'json']))

ns = kubectl('get', 'namespace', args.namespace)['metadata']
assert ns['uid'] == args.namespace_uid
assert ns['labels']['ops.platform.io/managed-by'] == 'opsctl-bootstrap'
assert ns['labels']['ops.platform.io/installation-id'] == args.installation_id
helm = ['helm', '--kube-context', args.context, '--namespace', args.namespace]
status = json.loads(subprocess.check_output([*helm, 'status', release, '-o', 'json']))
assert status['name'] == release and status['namespace'] == args.namespace
assert status['info']['status'] == 'failed', 'refuse active/successful release'
expected = {
    ('ServiceAccount', name, args.namespace), ('Service', name, args.namespace),
    ('Deployment', name, args.namespace), ('NetworkPolicy', name, args.namespace),
    ('ClusterRole', name, ''), ('ClusterRoleBinding', name, ''),
    ('ClusterRoleBinding', name + '-auth-delegator', ''),
    ('RoleBinding', name + '-auth-reader', 'kube-system'),
    ('APIService', 'v1beta1.metrics.k8s.io', ''),
}
observed = []
for obj in yaml.safe_load_all(status['manifest']):
    if not obj:
        continue
    meta = obj['metadata']
    identity = (obj['kind'], meta['name'], meta.get('namespace', ''))
    assert identity in expected, 'refuse resource outside fixed Metrics manifest'
    expected.remove(identity)
    assert 'helm.sh/hook' not in meta.get('annotations', {})
    words = ['get', identity[0], identity[1]]
    if identity[2]:
        words += ['-n', identity[2]]
    live = kubectl(*words)['metadata']
    assert live['uid'] and live['labels']['ops.platform.io/release'] == release
    assert live['labels']['app.kubernetes.io/managed-by'] == 'Helm'
    assert live['annotations']['meta.helm.sh/release-name'] == release
    assert live['annotations']['meta.helm.sh/release-namespace'] == args.namespace
    observed.append({'kind': identity[0], 'name': identity[1], 'namespace': identity[2], 'uid': live['uid']})
assert not expected, 'incomplete fixed Metrics manifest'
selector = 'ops.platform.io/release=' + release
assert not kubectl('get', 'pvc', '-n', args.namespace, '-l', selector)['items']
for pod in kubectl('get', 'pods', '-n', args.namespace, '-l', selector)['items']:
    assert pod['status']['phase'] == 'Pending'
    assert not pod['status'].get('containerStatuses'), 'refuse Pod that has started'
subprocess.run([*helm, 'uninstall', release, '--wait', '--timeout', '5m'], check=True)
print(json.dumps({'status': 'OWNED_FAILED_METRICS_RELEASE_REMOVED', 'namespaceUID': args.namespace_uid, 'installationID': args.installation_id, 'resources': observed, 'bootstrapTrustAndStoragePreserved': True}, indent=2))
