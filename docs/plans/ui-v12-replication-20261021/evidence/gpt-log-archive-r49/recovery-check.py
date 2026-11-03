"""Task-owned storage interruption and collector checkpoint recovery, always restoring storage."""
from pathlib import Path
import base64,datetime,json,socket,ssl,subprocess,time,urllib.parse,urllib.request
assert socket.gethostname()=='mssc'
w=Path('/data/ops-v12-native-visual-r24-20261026/r49');ns='ops-native-v12-r24-20261026';store='ops-v12-r49-victorialogs';node='/opt/ops-assistant-browser-20261014/node'
cred=json.loads((w/'private/source-http.json').read_text());header={'Authorization':'Basic '+base64.b64encode((cred['username']+':'+cred['password']).encode()).decode()};ctx=ssl.create_default_context(cafile=str(w/'private/tls.crt'));saved=json.loads((w/'live-success.json').read_text())['outcome']
def query(expression):
 req=urllib.request.Request('https://127.0.0.1:19438/select/logsql/query?'+urllib.parse.urlencode({'query':expression}),headers=header)
 with urllib.request.urlopen(req,context=ctx,timeout=4)as r:return [json.loads(x)for x in r.read(65536).decode().splitlines()if x]
def count():
 result=query('kubernetes.pod_uid:'+json.dumps(saved['resource']['uid'])+' AND _time:['+saved['window']['from']+','+saved['window']['to']+') | stats count() as n');return sum(int(x['n'])for x in result)
def run(*args):return subprocess.run(args,check=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=45).stdout.decode()
def browser(mode):
 import os
 env=os.environ.copy();env['MODE']=mode
 output=subprocess.run([node,str(w/'check-history.mjs')],env=env,check=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=45).stdout.decode();return json.loads(output)
report={'startedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'baselineClosedWindowCount':count(),'expectedPopulation':saved['result']['population']['count']}
assert report['baselineClosedWindowCount']==report['expectedPopulation']
paused=False
try:
 run('docker','pause',store);paused=True;report['pausedAt']=datetime.datetime.now(datetime.timezone.utc).isoformat()
 time.sleep(18)
 report['outage']=browser('outage')
 # Graceful collector shutdown flushes its in-memory pending queue to persistent disk.
 run('kubectl','rollout','restart','ds/ops-r49-log-collector','-n',ns)
 run('kubectl','rollout','status','ds/ops-r49-log-collector','-n',ns,'--timeout=35s')
 report['persistentQueueFiles']=sum(1 for p in (w/'collector-data').rglob('*')if p.is_file())
 assert (w/'collector-data/vlagent-kubernetes-checkpoints.json').is_file()
finally:
 if paused:run('docker','unpause',store)
 report['resumedAt']=datetime.datetime.now(datetime.timezone.utc).isoformat();(w/'recovery-progress.json').write_text(json.dumps(report,indent=2))
time.sleep(20)
report['history']=browser('recovery');report['closedWindowCountAfterRestart']=count();assert report['closedWindowCountAfterRestart']==report['baselineClosedWindowCount'],'checkpoint restart replayed closed-window records'
start=report['pausedAt'];end=report['resumedAt'];rows=query('kubernetes.pod_uid:'+json.dumps(saved['resource']['uid'])+' AND _time:['+start+','+end+') | limit 30');assert rows,'real events during outage were not recovered'
report['outageEventsRecovered']=len(rows);report['status']='passed';report['completeCoverage']=False;report['exactlyOnceClaim']=False
(w/'recovery-check.json').write_text(json.dumps(report,indent=2));print(json.dumps(report))
