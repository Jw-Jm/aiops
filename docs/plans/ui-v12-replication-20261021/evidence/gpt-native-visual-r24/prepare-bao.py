"""One-shot isolated canary from the repository chart; no existing workload edits."""
from pathlib import Path
import json
import os
import subprocess
import yaml

root = Path('/data/ops-v12-native-visual-r24-20261026')
namespace = 'ops-native-v12-r24-20261026'
source = root / 'source'
private = root / 'private'
private.mkdir(mode=0o700)
(root / 'evidence').mkdir()
data = root / 'bao-data'; data.mkdir(); os.chown(data, 100, 1000); data.chmod(0o770)
subprocess.run(['kubectl', 'create', '-f', '-'], input=json.dumps({'apiVersion':'v1','kind':'Namespace','metadata':{'name':namespace,'labels':{'ops.local/task':'ui-v12-native-visual-r24'}}}), text=True, check=True)
profile = yaml.safe_load(Path('/data/ops-ui-v12-mfa-20261022-r1/gpt-login-r15/preview-r2/resolved-profile.yaml').read_text())
profile['components']['keycloak'] = {'mode':'disabled'}
profile['components']['openbao']['endpoint'] = f'https://ops-openbao.{namespace}.svc.cluster.local:8200'
profile['components']['openbao']['namespace'] = namespace
profile['components']['openbao']['version'] = '2.7.0'
profile['components']['openbao']['image'] = 'ghcr.io/openbao/openbao@sha256:71156a1c6623a5fa3f5e61b0c6a8ead0faf0df29a778339188443551995d1315'
profile['components']['openbao']['digest'] = 'sha256:71156a1c6623a5fa3f5e61b0c6a8ead0faf0df29a778339188443551995d1315'
(root/'resolved-profile.yaml').write_text(yaml.safe_dump(profile,allow_unicode=True))
values={'components':{'openbao':{'image':profile['components']['openbao']['image']}}}
(root/'bao-values.json').write_text(json.dumps(values))
raw = subprocess.check_output(['helm','template','ops-r24',str(source/'deploy/charts/ops-dependencies'),'-n',namespace,'-f',str(root/'bao-values.json'),'-s','templates/openbao.yaml'],text=True)
documents=list(yaml.safe_load_all(raw))
for doc in documents:
    if not doc: continue
    if doc['kind']=='StatefulSet':
        spec=doc['spec']['template']['spec']
        # Canary-only dedicated local storage; the shared StorageClasses/PVs and
        # every previous release remain untouched. Production PVC path is not claimed.
        doc['spec'].pop('volumeClaimTemplates')
        spec['automountServiceAccountToken']=False
        spec['volumes'].extend([
            {'name':'data','hostPath':{'path':str(data),'type':'Directory'}},
            {'name':'reviewer','projected':{'defaultMode':0o444,'sources':[
                {'serviceAccountToken':{'path':'token','expirationSeconds':600}},
                {'configMap':{'name':'kube-root-ca.crt','items':[{'key':'ca.crt','path':'ca.crt'}]}},
                {'downwardAPI':{'items':[{'path':'namespace','fieldRef':{'fieldPath':'metadata.namespace'}}]}}
            ]}}])
        spec['containers'][0]['volumeMounts'].append({'name':'reviewer','mountPath':'/var/run/secrets/kubernetes.io/serviceaccount','readOnly':True})
    doc.setdefault('metadata',{})['namespace']=namespace if doc['kind']!='ClusterRoleBinding' else ''
    if doc['kind']=='ClusterRoleBinding': doc['metadata'].pop('namespace',None)
manifest='\n---\n'.join(yaml.safe_dump(d) for d in documents if d)
(root/'bao-canary.yaml').write_text(manifest)
subprocess.run(['kubectl','create','-f','-'],input=manifest,text=True,check=True)
print(json.dumps({'namespace':namespace,'chart':'ops-dependencies/templates/openbao.yaml','projectedReviewerTTL':600,'storage':'task-owned hostPath; not a PVC/HA acceptance','oidcWorkloads':0}))
