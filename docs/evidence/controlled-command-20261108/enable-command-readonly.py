"""Prepare current's bounded read-only command acceptance; never submit a command.

Run on the deployment host after the independent current backup. The profile
must still be signed and published through the product's native MFA flow.
"""
import base64
import json
from pathlib import Path
import subprocess
import uuid

from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

ROOT = Path('/data/ops-current')
NS = 'ops-current'
POD = 'ops-metrics-server-metrics-6d46c4f8f5-7ztrn'
UID = '9d925158-f0c9-4ce5-84b8-830959e148dc'
TENANT = '01a1ab0b-3e7c-706e-bef6-024eeaf4c1b3'
CLUSTER = '79014400-9453-4cb4-9678-0fae47981d79'
IMAGE = 'ops.local/current/command-runner@sha256:bef1188002556accd30604c18a0e57c27bd0afe8b0168bc216421d3b8486f310'
NAME = 'ops-command-read-metrics'


def kube(kind, name):
    return json.loads(subprocess.check_output(['kubectl', '-n', NS, 'get', kind, name, '-o', 'json']))


def apply(obj):
    subprocess.run(['kubectl', 'apply', '-f', '-'], input=json.dumps(obj), text=True, check=True, stdout=subprocess.DEVNULL)


def resource(kind, name, **fields):
    version = 'rbac.authorization.k8s.io/v1' if kind in ('Role', 'RoleBinding') else 'v1'
    return dict(apiVersion=version, kind=kind, metadata=dict(name=name, namespace=NS), **fields)


assert kube('pod', POD)['metadata']['uid'] == UID, 'Target was recreated; review a new profile'
assert (ROOT / 'backups/20261108T103430Z/').is_dir(), 'Required independent backup missing'
stage = ROOT / 'build/command-closure-20261108'
profile_path = stage / 'profile.json'
if profile_path.exists():
    profile = json.loads(profile_path.read_text())
else:
    profile = json.loads((ROOT / 'config/execution-profile.json').read_text())
    profile.update(executionProfileId=str(uuid.uuid4()), name='current-metrics-pod-read',
                   allowedTargets=[f'k8s+v1://{TENANT}/{CLUSTER}/core/Pod/{UID}'],
                   toolImageDigest=IMAGE, networkPolicyRef=NAME,
                   credentialRef=f'serviceaccount:/{NS}/{NAME}', timeoutSeconds=60)
    profile_path.write_text(json.dumps(profile, indent=2))
assert profile['allowedTargets'] == [f'k8s+v1://{TENANT}/{CLUSTER}/core/Pod/{UID}']
assert profile['toolImageDigest'] == IMAGE
payload = dict(domain='ops-execution-profile/v2', tenantId=TENANT, profile=profile)
key = Ed25519PrivateKey.from_private_bytes((ROOT / 'private/registry-key').read_bytes()[:32])
signature = key.sign(json.dumps(payload, sort_keys=True, separators=(',', ':'), ensure_ascii=False).encode())
(stage / 'publication.json').write_text(json.dumps(dict(profile=profile, signerKeyId='current-registry', signature=base64.b64encode(signature).decode()), indent=2))

apply(resource('ServiceAccount', NAME, automountServiceAccountToken=False))
apply(resource('ServiceAccount', 'ops-command-runner', automountServiceAccountToken=False))
apply(resource('Role', NAME, rules=[dict(apiGroups=[''], resources=['pods'], resourceNames=[POD], verbs=['get'])]))
apply(resource('RoleBinding', NAME, roleRef=dict(apiGroup='rbac.authorization.k8s.io', kind='Role', name=NAME), subjects=[dict(kind='ServiceAccount', name=NAME, namespace=NS)]))
apply(resource('Role', NAME+'-token', rules=[dict(apiGroups=[''], resources=['serviceaccounts/token'], resourceNames=[NAME], verbs=['create'])]))
apply(resource('RoleBinding', NAME+'-token', roleRef=dict(apiGroup='rbac.authorization.k8s.io', kind='Role', name=NAME+'-token'), subjects=[dict(kind='ServiceAccount', name='ops-api', namespace=NS)]))
apply(resource('Role', 'ops-sp07-job-controller', rules=[dict(apiGroups=['batch'], resources=['jobs'], verbs=['create', 'get', 'delete'])]))
apply(resource('RoleBinding', 'ops-sp07-job-controller', roleRef=dict(apiGroup='rbac.authorization.k8s.io', kind='Role', name='ops-sp07-job-controller'), subjects=[dict(kind='ServiceAccount', name='ops-worker', namespace=NS)]))
apply(resource('ConfigMap', 'ops-command-runner-trust', data={
    'runner.json': json.dumps(dict(Namespace=NS, OpenBaoAddress=f'https://ops-openbao.{NS}.svc.cluster.local:8200', OpenBaoServerName=f'ops-openbao.{NS}.svc.cluster.local')),
    'openbao-ca.pem': (ROOT / 'private/bao-ca.pem').read_text(),
}))
# The existing context-signing Secret contains signing keys, not a PKI CA.
# This dedicated Secret contains only the already pinned public workload CA.
apply(resource('Secret', 'ops-command-runner-ca', data={'ca.pem': base64.b64encode((ROOT / 'config/workload-ca.pem').read_bytes()).decode()}))
selector = {'app.kubernetes.io/name': 'ops-command-runner'}
apply(dict(apiVersion='networking.k8s.io/v1', kind='NetworkPolicy', metadata=dict(name='ops-sp07-runner-default-deny', namespace=NS), spec=dict(podSelector=dict(matchLabels=selector), policyTypes=['Ingress', 'Egress'])))
apply(dict(apiVersion='networking.k8s.io/v1', kind='NetworkPolicy', metadata=dict(name=NAME, namespace=NS), spec=dict(
    podSelector=dict(matchLabels={**selector, 'ops.platform/network-profile': NAME}), policyTypes=['Ingress', 'Egress'], egress=[
        dict(to=[dict(ipBlock=dict(cidr='192.168.0.63/32'))], ports=[dict(protocol='TCP', port=18595), dict(protocol='TCP', port=6443)]),
        dict(to=[dict(podSelector=dict(matchLabels={'app.kubernetes.io/name': 'ops-openbao'}))], ports=[dict(protocol='TCP', port=8200)]),
        dict(to=[dict(namespaceSelector=dict(matchLabels={'kubernetes.io/metadata.name': 'kube-system'}), podSelector=dict(matchLabels={'k8s-app': 'kube-dns'}))], ports=[dict(protocol='UDP', port=53), dict(protocol='TCP', port=53)]),
    ])))
sp = dict(schemaVersion='sp07-runtime/v1', namespace=NS, listenAddress='0.0.0.0:18595',
          apiEndpoint=f'https://ops-api.{NS}.svc.cluster.local:18595', workloadCAFile='/etc/ops/runtime/workload-ca.pem',
          policyName='current-read-only', kubernetesEndpoint='https://192.168.0.63:6443',
          kubernetesCAFile='/var/run/secrets/ops-platform/execution/ca.pem',
          kubernetesTokenFile='/var/run/secrets/ops-platform/execution/token', trustConfigMap='ops-command-runner-trust',
          identitySecret='ops-command-runner-ca', tenants=[TENANT], admittedProfiles=[profile])
(stage / 'sp07-before.json').write_text((ROOT / 'config/sp07.json').read_text()) if not (stage / 'sp07-before.json').exists() else None
(ROOT / 'config/sp07.json').write_text(json.dumps(sp, indent=2))
cm = kube('cm', 'ops-runtime')
cm['data']['sp07.json'] = json.dumps(sp)
apply(cm)
svc = kube('svc', 'ops-api')
svc['spec']['ports'] = [p for p in svc['spec']['ports'] if p['name'] != 'command'] + [dict(name='command', port=18595, targetPort=18595)]
apply(svc)
for duty in ('api', 'worker'):
    dep = kube('deploy', 'ops-'+duty)
    spec = dep['spec']['template']['spec']
    spec['volumes'] = [v for v in spec['volumes'] if v['name'] != 'execution-token'] + [dict(name='execution-token', projected=dict(defaultMode=0o444, sources=[dict(serviceAccountToken=dict(path='token', expirationSeconds=600)), dict(configMap=dict(name='kube-root-ca.crt', items=[dict(key='ca.crt', path='ca.pem')]))]))]
    container = spec['containers'][0]
    container['volumeMounts'] = [v for v in container['volumeMounts'] if v['name'] != 'execution-token'] + [dict(name='execution-token', mountPath='/var/run/secrets/ops-platform/execution', readOnly=True)]
    dep['spec']['template']['metadata']['annotations']['ops.platform.io/sp07'] = 'readonly-acceptance-20261108'
    apply(dep)
print('Prepared bounded read-only execution infrastructure; profile publication and command confirmation are still required.')
