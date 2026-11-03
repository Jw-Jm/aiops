import json,subprocess,sys,hashlib,datetime
from pathlib import Path
w=Path('/data/ops-v12-native-visual-r24-20261026/r37');ns='ops-native-v12-r24-20261026'
artifact=sys.argv[1];assert artifact.startswith('ops-v12-r37') and artifact.endswith('-worker')
p=json.loads(subprocess.check_output(['kubectl','get','pod','ops-worker','-n',ns,'-o','json']))
m={'apiVersion':'v1','kind':'Pod','metadata':{'name':'ops-worker','namespace':ns,'labels':p['metadata'].get('labels',{})},'spec':p['spec']}
c=m['spec']['containers'][0];old=c['command'][0];mount=next(x for x in c['volumeMounts'] if x['mountPath']==old);v=next(x for x in m['spec']['volumes'] if x['name']==mount['name']);v['hostPath']['path']=str(w/artifact);mount['mountPath']=str(w/artifact);c['command']=[str(w/artifact)]
subprocess.run(['kubectl','delete','pod','ops-worker','-n',ns,'--wait=true'],check=True,stdout=subprocess.DEVNULL)
subprocess.run(['kubectl','create','-f','-'],input=json.dumps(m).encode(),check=True,stdout=subprocess.DEVNULL)
subprocess.run(['kubectl','wait','--for=condition=Ready','pod/ops-worker','-n',ns,'--timeout=120s'],check=True,stdout=subprocess.DEVNULL)
current=json.loads(subprocess.check_output(['kubectl','get','pod','ops-worker','-n',ns,'-o','json']))
receipt={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'artifact':artifact,'sha256':hashlib.sha256((w/artifact).read_bytes()).hexdigest(),'podUID':current['metadata']['uid']}
(w/(artifact+'-runtime.json')).write_text(json.dumps(receipt,indent=2));print(json.dumps(receipt))
