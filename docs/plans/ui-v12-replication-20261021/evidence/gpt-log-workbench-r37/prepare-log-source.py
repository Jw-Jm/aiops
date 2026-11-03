"""Import one bounded snapshot of actual task Pod logs into a private source."""
from pathlib import Path
import base64, datetime, hashlib, json, os, re, secrets, socket, ssl, subprocess, urllib.request, urllib.parse
assert socket.gethostname() == 'mssc'
root=Path('/data/ops-v12-native-visual-r24-20261026'); work=root/'r37'; private=work/'private'
private.mkdir(mode=0o700); (work/'victoria-data').mkdir(mode=0o700)
ns='ops-native-v12-r24-20261026'; pod='ops-seaweedfs-0'
def run(args): return subprocess.check_output(args,stderr=subprocess.PIPE)
meta=json.loads(run(['kubectl','get','pod',pod,'-n',ns,'-o','json']))
assert all(c.get('restartCount',0)==0 for c in meta['status']['containerStatuses'])
raw=run(['kubectl','logs',pod,'-n',ns,'--timestamps','--since=24h','--tail=-1'])
assert 0<len(raw)<2**20,'snapshot must be nonempty and bounded'
lines=[]
for line in raw.decode().splitlines():
 at,_,message=line.partition(' ')
 try: timestamp=datetime.datetime.fromisoformat(at.replace('Z','+00:00'))
 except ValueError: raise AssertionError('missing native log timestamp')
 # No credentials or secret-bearing lines are admitted to the task source.
 if re.search(r'(?i)(?:sk-[A-Za-z0-9]{16,}|(?:password|token|secret|authorization|recovery)["\s]*[:=])',message): continue
 level='unknown'
 if re.match(r'^I\d{4}',message) or re.search(r'(?i)\b(?:info|INFO)\b',message):level='info'
 elif re.match(r'^W\d{4}',message) or re.search(r'(?i)\bwarn(?:ing)?\b',message):level='warn'
 elif re.match(r'^E\d{4}',message) or re.search(r'(?i)\berror\b',message):level='error'
 lines.append((timestamp,at,message,level))
assert lines,'no safe native log observations'
end=max(x[0] for x in lines)+datetime.timedelta(seconds=1); start=end-datetime.timedelta(minutes=30)
lines=[x for x in lines if start<=x[0]<end]
after=json.loads(run(['kubectl','get','pod',pod,'-n',ns,'-o','json']))
assert meta['metadata']['uid']==after['metadata']['uid'] and meta['status']['containerStatuses']==after['status']['containerStatuses'],'Pod changed during capture'
cluster=json.loads((root/'r31/cluster-registration.json').read_text())['cluster']
# The tenant is read from the task registration evidence, never guessed.
registration=json.loads((root/'r31/source-registration.json').read_text())['source']
tenant=registration['dataScopeMapping'].get('nativeTenant')
if not tenant:
 cfg=json.loads((root/'r35/sp04-second-namespace.json').read_text());tenant=cfg['Clusters'][0]['Tenant']
records=[{'_time':at,'_msg':message,'tenant':tenant,'cluster':cluster['clusterUid'],'namespace':ns,'uid':meta['metadata']['uid'],'level':level} for _,at,message,level in lines]
payload=('\n'.join(json.dumps(r,ensure_ascii=False) for r in records)+'\n').encode()
(private/'native-logs.ndjson').write_bytes(payload);os.chmod(private/'native-logs.ndjson',0o600)
password=secrets.token_hex(32);(private/'http-password').write_text(password);os.chmod(private/'http-password',0o600)
credential={'schemaVersion':'ops-source-http-credentials/v1','mode':'basic','username':'r37-snapshot','password':password}
(private/'source-http.json').write_text(json.dumps(credential));os.chmod(private/'source-http.json',0o600)
run(['openssl','req','-x509','-newkey','ec','-pkeyopt','ec_paramgen_curve:P-256','-nodes','-days','2','-subj','/CN=r37-task-log-snapshot','-addext','subjectAltName=IP:127.0.0.1,DNS:localhost','-keyout',str(private/'tls.key'),'-out',str(private/'tls.crt')]);os.chmod(private/'tls.key',0o600)
subprocess.run(['docker','run','-d','--name','ops-v12-r37-victorialogs','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--pids-limit','64','--memory','256m','--cpus','0.5','-p','127.0.0.1:19428:9428','--mount',f'type=bind,source={private},target=/private,readonly','--mount',f'type=bind,source={work/"victoria-data"},target=/victoria-logs-data','ops.local/amd64-poc/victoria-logs:v1.52.0','-httpListenAddr=:9428','-tls','-tlsCertFile=/private/tls.crt','-tlsKeyFile=/private/tls.key','-httpAuth.username=r37-snapshot','-httpAuth.password=file:///private/http-password','-retentionPeriod=7d'],check=True,stdout=subprocess.DEVNULL)
ctx=ssl.create_default_context(cafile=str(private/'tls.crt'))
header={'Authorization':'Basic '+base64.b64encode(('r37-snapshot:'+password).encode()).decode()}
request=urllib.request.Request('https://127.0.0.1:19428/insert/jsonline?_stream_fields=tenant,cluster,namespace,uid,level',data=payload,headers={**header,'Content-Type':'application/stream+json'})
with urllib.request.urlopen(request,context=ctx,timeout=10) as response:assert response.status==200
# Store the real source credential in this task's Bao, not a placeholder URI.
baohost=f'ops-openbao.{ns}.svc.cluster.local';baoip=run(['kubectl','get','svc','ops-openbao','-n',ns,'-o','jsonpath={.spec.clusterIP}']).decode();lookup=socket.getaddrinfo
socket.getaddrinfo=lambda name,*args,**kwargs:lookup(baoip if name==baohost else name,*args,**kwargs)
token=json.loads((root/'private/bao-recovery.json').read_text())['rootToken']
request=urllib.request.Request(f'https://{baohost}:8200/v1/secret/data/ops/r37-victorialogs/source',data=json.dumps({'data':credential}).encode(),headers={'X-Vault-Token':token,'Content-Type':'application/json'},method='POST')
with urllib.request.urlopen(request,context=ssl.create_default_context(cafile=str(root/'private/bao-ca.pem')),timeout=10) as response:assert response.status==200
receipt={'mode':'bounded_native_pod_log_snapshot','namespace':ns,'pod':pod,'podUID':meta['metadata']['uid'],'containerIDs':[c['containerID'] for c in meta['status']['containerStatuses']],'cluster':cluster,'tenantId':tenant,'window':{'from':start.isoformat(),'to':end.isoformat()},'records':len(records),'dataSHA256':hashlib.sha256(payload).hexdigest(),'collectorVersion':'kubernetes-log-snapshot/v1','coverageComplete':False,'authRef':'openbao://secret/data/ops/r37-victorialogs/source','endpoint':'https://127.0.0.1:19428','limitation':'One captured task Pod and closed half-open window; no claim of continuous or cluster-wide log collection.'}
(work/'source-snapshot.json').write_text(json.dumps(receipt,indent=2));print(json.dumps({k:receipt[k] for k in ['mode','podUID','records','window','coverageComplete','limitation']}))
