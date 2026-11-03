"""Task-only live collector fault. Restore the projected volume even on failure."""
import json,os,socket,subprocess,time
from pathlib import Path
assert socket.gethostname()=='mssc'
root=Path('/data/ops-v12-native-visual-r24-20261026');work=root/'r35';ns='ops-native-v12-r24-20261026'
work.mkdir(mode=0o700,exist_ok=True)
def kube(*args,**kwargs):return subprocess.check_output(['kubectl',*args],**kwargs)
def restart(pod):
 subprocess.run(['kubectl','delete','pod','ops-worker','-n',ns,'--wait=true'],check=True,stdout=subprocess.DEVNULL)
 subprocess.run(['kubectl','create','-f','-'],input=json.dumps(pod).encode(),check=True,stdout=subprocess.DEVNULL)
 subprocess.run(['kubectl','wait','--for=condition=Ready','pod/ops-worker','-n',ns,'--timeout=120s'],check=True,stdout=subprocess.DEVNULL)
def probe(name,count=1):
 env=os.environ|{'OUTPUT':str(work/(name+'.json')),'COUNT':str(count)}
 subprocess.run(['/opt/ops-assistant-browser-20261014/node',str(work/'resource-probe.mjs')],env=env,check=True)
 return json.loads((work/(name+'.json')).read_text())
pod=json.loads(kube('get','pod','ops-worker','-n',ns,'-o','json'))
original={'apiVersion':'v1','kind':'Pod','metadata':{'name':'ops-worker','namespace':ns,'labels':pod['metadata'].get('labels',{})},'spec':pod['spec']}
(work/'worker-before.json').write_text(json.dumps(original,indent=2));os.chmod(work/'worker-before.json',0o600)
credential=work/'credential-probe';credential.mkdir(mode=0o755,exist_ok=True)
valid=kube('create','token','ops-worker','-n',ns,'--duration=600s')
(credential/'token').write_bytes(valid);os.chmod(credential/'token',0o444)
fault=json.loads(json.dumps(original))
for volume in fault['spec']['volumes']:
 if volume['name']=='r32-cluster-token':volume.clear();volume.update({'name':'r32-cluster-token','hostPath':{'path':str(credential),'type':'Directory'}})
result={'namespace':ns,'changes':'only task Worker token mount; original projected identity restored','stages':[]}
start=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime())
try:
 manifest={'apiVersion':'v1','kind':'ConfigMap','metadata':{'name':'r35-credential-proof','namespace':ns,'labels':{'ops.platform.io/revision':'before'}},'data':{'purpose':'task-only live credential recovery proof'}}
 subprocess.run(['kubectl','apply','-f','-'],input=json.dumps(manifest).encode(),check=True,stdout=subprocess.DEVNULL)
 restart(fault)
 deadline=time.monotonic()+90
 result['startupObservations']=[]
 while True:
  before=probe('credential-before')
  result['startupObservations'].extend(before['requests'])
  if before['requests'][0]['status']==200 and any(r['labels'].get('ops.platform.io/revision')=='before' for r in before['requests'][0]['rows']):break
  assert time.monotonic()<deadline,'valid credential collector did not qualify'
  time.sleep(3)
 result['stages'].append('valid temporary600s token for the same task SA: persisted ConfigMap before')
 # A syntactically shaped but unsigned token must reach Kubernetes and be refused.
 os.chmod(credential/'token',0o600);(credential/'token').write_text('eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJpbnZhbGlkLXI zNSJ9.invalid-signature'.replace(' ',''));os.chmod(credential/'token',0o444)
 restart(fault)
 kube('label','configmap','r35-credential-proof','-n',ns,'ops.platform.io/revision=during-invalid','--overwrite')
 time.sleep(5)
 denied=probe('credential-invalid')
 assert denied['requests'][0]['status']==503, 'invalid credentials did not close the graph read'
 logs=kube('logs','ops-worker','-n',ns,'--since-time='+start).decode()
 (work/'credential-invalid-worker.log').write_text(logs)
 assert 'Unauthorized' in logs or 'unauthorized' in logs or 'credential' in logs
 result['stages'].append('invalid credential: Kubernetes refusal and resource HTTP503; no successful current fact read')
 # Restore real kubelet projection, never mint a new permanent credential.
 restart(original)
 deadline=time.monotonic()+90
 while True:
  recovered=probe('credential-recovered')
  requests=recovered['requests']
  if requests[0]['status']==200 and any(r['labels'].get('ops.platform.io/revision')=='during-invalid' for r in requests[0]['rows']):break
  assert time.monotonic()<deadline,'collector did not recover the missed update'
  time.sleep(3)
 kube('label','configmap','r35-credential-proof','-n',ns,'ops.platform.io/revision=after-recovery','--overwrite')
 time.sleep(3)
 stable=probe('credential-stable',12)
 assert all(x['status']==200 and len(x['rows'])==1 and x['rows'][0]['labels'].get('ops.platform.io/revision')=='after-recovery' for x in stable['requests'])
 result['stages'].append('projected token restored: missed update recovered, later update persisted; 12 consecutive HTTP200 reads')
 result['status']='passed'
finally:
 current=json.loads(kube('get','pod','ops-worker','-n',ns,'-o','json'))
 volume=next(v for v in current['spec']['volumes'] if v['name']=='r32-cluster-token')
 if 'projected' not in volume:restart(original)
 result['originalProjectionRestored']=True
 (work/'credential-recovery.json').write_text(json.dumps(result,indent=2))
 print(json.dumps(result))
