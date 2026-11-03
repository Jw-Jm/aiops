"""Use real application version output to test container restart and same-name Pod recreation."""
from pathlib import Path
import os,base64,datetime,json,socket,ssl,subprocess,time,urllib.parse,urllib.request
assert socket.gethostname()=='mssc'
w=Path('/data/ops-v12-native-visual-r24-20261026/r49');ns='ops-native-v12-r24-20261026';name='ops-r49-log-identity'
def kube(*args):return subprocess.check_output(['kubectl',*args],stderr=subprocess.PIPE)
image=json.loads(kube('get','pod','ops-seaweedfs-0','-n',ns,'-o','json'))['spec']['containers'][0]['image']
manifest={'apiVersion':'v1','kind':'Pod','metadata':{'name':name,'namespace':ns},'spec':{'restartPolicy':'Always','automountServiceAccountToken':False,'containers':[{'name':n,'image':image,'imagePullPolicy':'Never','command':['weed','version'],'resources':{'requests':{'cpu':'5m','memory':'16Mi'},'limits':{'cpu':'100m','memory':'64Mi'}},'securityContext':{'allowPrivilegeEscalation':False,'readOnlyRootFilesystem':True,'capabilities':{'drop':['ALL']}}}for n in ['version-a','version-b']]}}
cred=json.loads((w/'private/source-http.json').read_text());header={'Authorization':'Basic '+base64.b64encode((cred['username']+':'+cred['password']).encode()).decode()};ctx=ssl.create_default_context(cafile=str(w/'private/tls.crt'))
def events(uid):
 req=urllib.request.Request('https://127.0.0.1:19438/select/logsql/query?'+urllib.parse.urlencode({'query':'kubernetes.pod_name:'+json.dumps(name)+' AND kubernetes.pod_uid:'+json.dumps(uid)+' | limit 30'}),headers=header)
 with urllib.request.urlopen(req,context=ctx,timeout=4)as r:return [json.loads(x)for x in r.read(65536).decode().splitlines()if x]
def create():
 subprocess.run(['kubectl','create','-f','-'],input=json.dumps(manifest).encode(),check=True,stdout=subprocess.DEVNULL)
 return json.loads(kube('get','pod',name,'-n',ns,'-o','json'))['metadata']['uid']
created=False;report={}
try:
 uid=create();created=True;report['firstPodUID']=uid
 for i in range(12):
  time.sleep(5);rows=events(uid);names={r['kubernetes.container_name']for r in rows};ids={r['kubernetes.container_id']for r in rows}
  if names=={'version-a','version-b'}and len(ids)>=4:break
 assert names=={'version-a','version-b'}and len(ids)>=4,'both containers and restart identities not observed'
 assert all(any('version' in r['_msg'].lower() and r['kubernetes.container_name']==n for r in rows)for n in names)
 report.update(firstEvents=len(rows),containerNames=sorted(names),containerIDs=sorted(ids),restartIdentityObserved=True)
 env=os.environ.copy();env.update(MODE='success',CASE='multi-container',POD_UID=uid)
 subprocess.run(['/opt/ops-assistant-browser-20261014/node',str(w/'live-query.mjs')],env=env,check=True,stdout=subprocess.DEVNULL,timeout=45)
 report['platformQuery']=json.loads((w/'live-multi-container.json').read_text())['outcome']['queryId']
 kube('delete','pod',name,'-n',ns,'--wait=true','--timeout=20s');created=False
 new=create();created=True;assert uid!=new
 for i in range(8):
  time.sleep(4);newrows=events(new)
  if len({r['kubernetes.container_name']for r in newrows})==2:break
 assert newrows and all(r['kubernetes.pod_uid']==new for r in newrows)
 assert all(r['kubernetes.container_id']not in ids for r in newrows)
 newids={r['kubernetes.container_id']for r in newrows}
 assert all(r['kubernetes.container_id']not in newids for r in events(uid)),'recreated Pod polluted original UID selection'
 report.update(secondPodUID=new,secondEvents=len(newrows),sameNameIsolated=True,status='passed',checkedAt=datetime.datetime.now(datetime.timezone.utc).isoformat())
finally:
 if created:kube('delete','pod',name,'-n',ns,'--wait=true','--timeout=20s')
 (w/'identity-check.json').write_text(json.dumps(report,indent=2))
print(json.dumps(report))
