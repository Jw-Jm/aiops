"""Advance only this task Worker to the reviewed r51c archive scheduling fix."""
from pathlib import Path
import datetime,hashlib,json,socket,subprocess
assert socket.gethostname()=='mssc'
w=Path('/data/ops-v12-native-visual-r24-20261026/r51');ns='ops-native-v12-r24-20261026';artifact=w/'ops-r51c-worker';assert artifact.is_file()
p=json.loads(subprocess.check_output(['kubectl','get','pod','ops-worker','-n',ns,'-o','json']));m={'apiVersion':'v1','kind':'Pod','metadata':{'name':'ops-worker','namespace':ns,'labels':p['metadata'].get('labels',{})},'spec':p['spec']}
c=m['spec']['containers'][0];old=c['command'][0];mount=next(v for v in c['volumeMounts']if v['mountPath']==old);volume=next(v for v in m['spec']['volumes']if v['name']==mount['name']);new=str(artifact);mount['mountPath']=new;volume['hostPath']['path']=new;c['command']=[new]
subprocess.run(['kubectl','delete','pod','ops-worker','-n',ns,'--wait=true'],check=True,stdout=subprocess.DEVNULL);subprocess.run(['kubectl','create','-f','-'],input=json.dumps(m).encode(),check=True,stdout=subprocess.DEVNULL)
(w/'worker-r51c.json').write_text(json.dumps({'previousPodUID':p['metadata']['uid'],'artifact':new,'sha256':hashlib.sha256(artifact.read_bytes()).hexdigest(),'at':datetime.datetime.now(datetime.timezone.utc).isoformat()},indent=2));print('Worker advanced to r51c; other components preserved')
