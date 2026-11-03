"""Extend only the reviewed task source to the second task namespace."""
import json,os,socket,subprocess,time
from pathlib import Path
assert socket.gethostname()=='mssc'
root=Path('/data/ops-v12-native-visual-r24-20261026');work=root/'r35';ns='ops-native-v12-r24-20261026'
def kube(*args):return subprocess.check_output(['kubectl',*args])
for name in ['ops-native-v12-r35-scope','ops-native-v12-r35-outside']:
 manifest={'apiVersion':'v1','kind':'Namespace','metadata':{'name':name,'labels':{'ops.platform.io/task':'ui-v12-r35'}}}
 subprocess.run(['kubectl','create','-f','-'],input=json.dumps(manifest).encode(),check=True,stdout=subprocess.DEVNULL)
for name in [ns,'ops-native-v12-r35-scope','ops-native-v12-r35-outside']:
 manifest={'apiVersion':'v1','kind':'ConfigMap','metadata':{'name':'r35-same-name','namespace':name,'labels':{'ops.platform.io/namespace-proof':name}},'data':{'purpose':'task scoped same-name resource proof'}}
 subprocess.run(['kubectl','create','-f','-'],input=json.dumps(manifest).encode(),check=True,stdout=subprocess.DEVNULL)
receipt=json.loads((work/'namespace-source.json').read_text())
assert receipt['status']=='passed'
cm=json.loads(kube('get','cm','r28-runtime','-n',ns,'-o','json'))
(work/'sp04-before-namespace.json').write_text(cm['data']['sp04.json'])
config=json.loads(cm['data']['sp04.json']);cluster=config['Clusters'][0]
assert cluster['SourceID']==receipt['outcome']['sourceId']
cluster['Namespaces']=[ns,'ops-native-v12-r35-scope'];cluster['SourceRevision']=receipt['outcome']['revision']
cm['data']['sp04.json']=json.dumps(config,indent=1)
subprocess.run(['kubectl','replace','-f','-'],input=json.dumps(cm).encode(),check=True,stdout=subprocess.DEVNULL)
(work/'sp04-second-namespace.json').write_text(cm['data']['sp04.json'])
pod=json.loads(kube('get','pod','ops-worker','-n',ns,'-o','json'))
manifest={'apiVersion':'v1','kind':'Pod','metadata':{'name':'ops-worker','namespace':ns,'labels':pod['metadata'].get('labels',{})},'spec':pod['spec']}
assert any('projected' in v for v in manifest['spec']['volumes'] if v['name']=='r32-cluster-token')
subprocess.run(['kubectl','delete','pod','ops-worker','-n',ns,'--wait=true'],check=True,stdout=subprocess.DEVNULL)
subprocess.run(['kubectl','create','-f','-'],input=json.dumps(manifest).encode(),check=True,stdout=subprocess.DEVNULL)
subprocess.run(['kubectl','wait','--for=condition=Ready','pod/ops-worker','-n',ns,'--timeout=120s'],check=True,stdout=subprocess.DEVNULL)
env=os.environ|{'PGPASSWORD':(root/'private/pg-password').read_text().strip()}
query="SELECT COALESCE(json_agg(json_build_object('canonicalId',canonical_id,'namespace',namespace,'name',name,'metadata',metadata)), '[]'::json) FROM platform.resource_entities WHERE tenant_id='01a177f1-372f-75b7-be90-e8121d1445f5' AND name='r35-same-name' AND deleted_at IS NULL"
deadline=time.monotonic()+100
while True:
 rows=json.loads(subprocess.check_output(['docker','exec','-e','PGPASSWORD','ops-v12-native-r24-pg','psql','-U','postgres','-d','ui_v12_r24_native','-tA','-c',query],env=env))
 if {r['namespace'] for r in rows}=={ns,'ops-native-v12-r35-scope'}:break
 assert time.monotonic()<deadline,'second namespace not persisted'
 time.sleep(3)
proof={'status':'passed','sourceRevision':cluster['SourceRevision'],'rows':rows,'outsidePersisted':False,'physicalResources':[]}
for name in [ns,'ops-native-v12-r35-scope','ops-native-v12-r35-outside']:
 cm=json.loads(kube('get','cm','r35-same-name','-n',name,'-o','json'))
 proof['physicalResources'].append({'namespace':name,'uid':cm['metadata']['uid'],'resourceVersion':cm['metadata']['resourceVersion']})
(work/'namespace-persistence.json').write_text(json.dumps(proof,indent=2))
print(json.dumps({'status':'passed','persistedNamespaces':[r['namespace'] for r in rows],'outsidePersisted':False}))
