"""Forward the owned preview to native logs; preserve the snapshot and historical data."""
from pathlib import Path
import datetime,hashlib,json,socket,subprocess,os,urllib.parse
assert socket.gethostname()=='mssc'
base=Path('/data/ops-v12-native-visual-r24-20261026');w=base/'r48';ns='ops-native-v12-r24-20261026'
def kube(*args):return subprocess.check_output(['kubectl',*args])
assert json.loads(kube('get','namespace',ns,'-o','json'))['metadata']['uid']=='731f0e88-1893-4836-844b-a29374c1ca11'
private=w/'private';private.mkdir(mode=0o700)
cm=json.loads(kube('get','cm','r28-runtime','-n',ns,'-o','json'));config=json.loads(cm['data']['sp04.json'])
assert len(config['Clusters'])==1 and config['Clusters'][0]['SourceID']=='01a17c3c-ef66-70cf-9ff1-cad25a8bee34' and config['Clusters'][0]['SourceRevision']==3
assert [s['Binding']['SourceID'] for s in config['Sources']]==['01a1805f-2214-7373-82a4-c20276c68d43']
for kind in ['role','rolebinding']:assert not kube('get',kind,'r48-native-pod-logs','-n',ns,'--ignore-not-found','-o','name').strip()
(private/'runtime-before.json').write_text(json.dumps(cm));os.chmod(private/'runtime-before.json',0o600)
env=os.environ.copy();env['SP03_MIGRATION_DATABASE_URL']='postgresql://postgres:'+urllib.parse.quote((base/'private/pg-password').read_text().strip(),safe='')+'@127.0.0.1:15454/ui_v12_r24_native?sslmode=disable'
with (w/'migration.log').open('x') as log:subprocess.run([str(w/'ops-v12-r48-db-migrate'),'-dir',str(w/'source/migrations'),'-to','72'],env=env,stdout=log,stderr=subprocess.STDOUT,check=True)
role={'apiVersion':'rbac.authorization.k8s.io/v1','kind':'Role','metadata':{'name':'r48-native-pod-logs','namespace':ns,'labels':{'ops.platform/task':'v12-native-logs-r48'}},'rules':[{'apiGroups':[''],'resources':['pods/log'],'verbs':['get']}]}
binding={'apiVersion':'rbac.authorization.k8s.io/v1','kind':'RoleBinding','metadata':role['metadata'],'roleRef':{'apiGroup':'rbac.authorization.k8s.io','kind':'Role','name':'r48-native-pod-logs'},'subjects':[{'kind':'ServiceAccount','name':'ops-worker','namespace':ns}]}
for document in [role,binding]:subprocess.run(['kubectl','create','-f','-'],input=json.dumps(document).encode(),check=True,stdout=subprocess.DEVNULL)
config['Clusters'][0]['PodLogs']=True
config['Sources']=[] # Retire only the expired snapshot from current query routing.
cm['data']['sp04.json']=json.dumps(config,indent=2)
subprocess.run(['kubectl','replace','-f','-'],input=json.dumps(cm).encode(),check=True,stdout=subprocess.DEVNULL)
receipts=[]
for name,artifact in [('ops-worker','ops-v12-r48-worker'),('ops-api','ops-v12-r48-api')]:
 p=json.loads(kube('get','pod',name,'-n',ns,'-o','json'));assert any(x['type']=='Ready' and x['status']=='True' for x in p['status']['conditions'])
 (private/(name+'-before.json')).write_text(json.dumps(p));os.chmod(private/(name+'-before.json'),0o600)
 m={'apiVersion':'v1','kind':'Pod','metadata':{'name':name,'namespace':ns,'labels':p['metadata'].get('labels',{})},'spec':p['spec']}
 c=m['spec']['containers'][0];old=c['command'][0];mount=next(x for x in c['volumeMounts'] if x['mountPath']==old);v=next(x for x in m['spec']['volumes'] if x['name']==mount['name']);assert v['hostPath']['path'].startswith(str(base)+'/')
 v['hostPath']['path']=str(w/artifact);mount['mountPath']=str(w/artifact);c['command']=[str(w/artifact)]
 subprocess.run(['kubectl','delete','pod',name,'-n',ns,'--wait=true'],check=True,stdout=subprocess.DEVNULL)
 subprocess.run(['kubectl','create','-f','-'],input=json.dumps(m).encode(),check=True,stdout=subprocess.DEVNULL)
 subprocess.run(['kubectl','wait','--for=condition=Ready','pod/'+name,'-n',ns,'--timeout=120s'],check=True,stdout=subprocess.DEVNULL)
 current=json.loads(kube('get','pod',name,'-n',ns,'-o','json'))
 receipts.append({'component':name,'artifact':artifact,'sha256':hashlib.sha256((w/artifact).read_bytes()).hexdigest(),'previousArtifactPreserved':old,'previousPodUID':p['metadata']['uid'],'podUID':current['metadata']['uid']})
receipt={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'namespace':ns,'database':'ui_v12_r24_native','schema':72,'artifacts':receipts,'source':{'registrationId':config['Clusters'][0]['SourceID'],'revision':3,'mode':'kubernetes-pod-logs/v1','namespace':ns,'permission':'get pods/log only'},'retiredSnapshotConfigured':False}
(w/'backend-runtime.json').write_text(json.dumps(receipt,indent=2));print(json.dumps(receipt))
