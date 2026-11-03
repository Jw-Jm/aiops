"""Stop only the new collector, then restart the new TSDB; always restore owned Pods."""
from pathlib import Path
import base64,datetime,json,os,socket,ssl,subprocess,time,urllib.parse,urllib.request
assert socket.gethostname()=='mssc'
w=Path('/data/ops-v12-native-visual-r24-20261026/r51');ns='ops-native-v12-r24-20261026';node='/opt/ops-assistant-browser-20261014/node'
cred=json.loads((w/'private/source-http.json').read_text());headers={'Authorization':'Basic '+base64.b64encode((cred['username']+':'+cred['password']).encode()).decode()};ctx=ssl.create_default_context(cafile=str(w/'private/tls.crt'))
def kube(*args):return subprocess.check_output(['kubectl',*args],stderr=subprocess.PIPE,timeout=45)
def nativequery(q):
 req=urllib.request.Request('https://127.0.0.1:19448/api/v1/query?'+urllib.parse.urlencode({'query':q,'nocache':'1'}),headers=headers)
 with urllib.request.urlopen(req,context=ctx,timeout=5)as r:return json.load(r)
def browser(mode):
 env=os.environ.copy();env['MODE']=mode
 p=subprocess.run([node,str(w/'check-source.mjs')],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=45)
 assert p.returncode==0,p.stdout.decode();return json.loads((w/('check-'+mode+'.json')).read_text())
def manifest(name):
 p=json.loads(kube('get','pod',name,'-n',ns,'-o','json'));return {'apiVersion':'v1','kind':'Pod','metadata':{'name':name,'namespace':ns,'labels':p['metadata'].get('labels',{})},'spec':p['spec']}
def restore(m):
 subprocess.run(['kubectl','create','-f','-'],input=json.dumps(m).encode(),check=True,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE,timeout=15)
report={'startedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'cases':[]}
ksm=manifest('ops-r51-ksm');stopped=False
try:
 kube('delete','pod','ops-r51-ksm','-n',ns,'--wait=true','--timeout=30s');stopped=True
 time.sleep(50)
 data=nativequery('up{job="r51-kube-state"}');assert data['status']=='success'and all(float(r['value'][1])==0 for r in data['data']['result'])
 out=browser('outage');report['cases'].append({'case':'collector_stopped_with_tsdb_http_still_healthy','sourceHTTP':200,'qualificationWithdrawn':True,'queryStatus':out['queryStatus']})
finally:
 if stopped:restore(ksm)
time.sleep(30);assert browser('recovery')['status']=='passed';report['cases'].append({'case':'collector_restored_without_changing_source_proof','passed':True})
vm=manifest('ops-r51-metrics');stopped=False
before=nativequery('count_over_time(container_memory_working_set_bytes{collector="victoriametrics-kubernetes/v1",uid="0e648683-1088-4032-9b39-0a122ff475be"}[30m])');assert before['data']['result']
try:
 kube('delete','pod','ops-r51-metrics','-n',ns,'--wait=true','--timeout=30s');stopped=True
finally:
 if stopped:restore(vm)
time.sleep(35);after=browser('storage-recovery');assert after['status']=='passed';report['cases'].append({'case':'tsdb_recreated_with_same_persistent_data','passed':True,'queryStatus':after['queryStatus']})
report.update(status='passed',coverageComplete=False,completedAt=datetime.datetime.now(datetime.timezone.utc).isoformat());(w/'recovery-check.json').write_text(json.dumps(report,indent=2));print(json.dumps(report))
