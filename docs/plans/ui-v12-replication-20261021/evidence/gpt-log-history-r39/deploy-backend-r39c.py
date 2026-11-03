"""Forward only the two owned preview pods; database and shared configuration stay intact."""
from pathlib import Path
import datetime,hashlib,json,socket,subprocess
assert socket.gethostname()=='mssc'
w=Path('/data/ops-v12-native-visual-r24-20261026/r39');ns='ops-native-v12-r24-20261026'
def kube(*args):return subprocess.check_output(['kubectl',*args])
assert json.loads(kube('get','namespace',ns,'-o','json'))['metadata']['uid']=='731f0e88-1893-4836-844b-a29374c1ca11'
receipts=[]
# Old API requests remain compatible with the new Worker's unpaged list mode.
for name,artifact in [('ops-worker','ops-v12-r39c-worker'),('ops-api','ops-v12-r39c-api')]:
 p=json.loads(kube('get','pod',name,'-n',ns,'-o','json'))
 assert any(x['type']=='Ready' and x['status']=='True' for x in p['status']['conditions'])
 m={'apiVersion':'v1','kind':'Pod','metadata':{'name':name,'namespace':ns,'labels':p['metadata'].get('labels',{})},'spec':p['spec']}
 c=m['spec']['containers'][0];old=c['command'][0];mount=next(x for x in c['volumeMounts'] if x['mountPath']==old);v=next(x for x in m['spec']['volumes'] if x['name']==mount['name']);assert v['hostPath']['path'].startswith('/data/ops-v12-native-visual-r24-20261026/')
 v['hostPath']['path']=str(w/artifact);mount['mountPath']=str(w/artifact);c['command']=[str(w/artifact)]
 subprocess.run(['kubectl','delete','pod',name,'-n',ns,'--wait=true'],check=True,stdout=subprocess.DEVNULL)
 subprocess.run(['kubectl','create','-f','-'],input=json.dumps(m).encode(),check=True,stdout=subprocess.DEVNULL)
 subprocess.run(['kubectl','wait','--for=condition=Ready','pod/'+name,'-n',ns,'--timeout=120s'],check=True,stdout=subprocess.DEVNULL)
 current=json.loads(kube('get','pod',name,'-n',ns,'-o','json'))
 receipts.append({'component':name,'artifact':artifact,'sha256':hashlib.sha256((w/artifact).read_bytes()).hexdigest(),'previousArtifactPreserved':old,'previousPodUID':p['metadata']['uid'],'podUID':current['metadata']['uid']})
receipt={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'namespace':ns,'database':'ui_v12_r24_native','schema':71,'artifacts':receipts}
(w/'backend-runtime-r39c.json').write_text(json.dumps(receipt,indent=2));print(json.dumps(receipt))
