"""Resume after the initial TLS readiness failure; do not regenerate credentials."""
from pathlib import Path
import base64,datetime,hashlib,json,socket,ssl,subprocess,urllib.request,urllib.parse
assert socket.gethostname()=='mssc'
root=Path('/data/ops-v12-native-visual-r24-20261026');work=root/'r37';private=work/'private'
credential=json.loads((private/'source-http.json').read_text());payload=(private/'native-logs.ndjson').read_bytes();records=[json.loads(x) for x in payload.decode().splitlines()]
ctx=ssl.create_default_context(cafile=str(private/'tls.crt'));headers={'Authorization':'Basic '+base64.b64encode((credential['username']+':'+credential['password']).encode()).decode()}
request=urllib.request.Request('https://127.0.0.1:19428/select/logsql/stats_query',data=urllib.parse.urlencode({'query':'* | stats count() as value'}).encode(),headers={**headers,'Content-Type':'application/x-www-form-urlencoded'})
with urllib.request.urlopen(request,context=ctx,timeout=10) as response:prior=json.load(response)
counts=prior.get('data',{}).get('result',[]);assert not counts or float(counts[0]['value'][1])==0,'source already has rows; do not repeat import'
request=urllib.request.Request('https://127.0.0.1:19428/insert/jsonline?_stream_fields=tenant,cluster,namespace,uid,level',data=payload,headers={**headers,'Content-Type':'application/stream+json'})
with urllib.request.urlopen(request,context=ctx,timeout=10) as response:assert response.status==200
(work/'ingest-receipt.json').write_text(json.dumps({'status':200,'records':len(records),'dataSHA256':hashlib.sha256(payload).hexdigest()}))
ns=records[0]['namespace'];baohost=f'ops-openbao.{ns}.svc.cluster.local';baoip=subprocess.check_output(['kubectl','get','svc','ops-openbao','-n',ns,'-o','jsonpath={.spec.clusterIP}']).decode();lookup=socket.getaddrinfo
socket.getaddrinfo=lambda name,*args,**kwargs:lookup(baoip if name==baohost else name,*args,**kwargs)
token=json.loads((root/'private/bao-recovery.json').read_text())['rootToken']
request=urllib.request.Request(f'https://{baohost}:8200/v1/secret/data/ops/r37-victorialogs/source',data=json.dumps({'data':credential}).encode(),headers={'X-Vault-Token':token,'Content-Type':'application/json'},method='POST')
with urllib.request.urlopen(request,context=ssl.create_default_context(cafile=str(root/'private/bao-ca.pem')),timeout=10) as response:assert response.status==200
end=max(datetime.datetime.fromisoformat(r['_time'].replace('Z','+00:00')) for r in records)+datetime.timedelta(seconds=1);start=end-datetime.timedelta(minutes=30)
cluster=json.loads((root/'r31/cluster-registration.json').read_text())['cluster']
receipt={'mode':'bounded_native_pod_log_snapshot','namespace':ns,'pod':'ops-seaweedfs-0','podUID':records[0]['uid'],'cluster':cluster,'tenantId':records[0]['tenant'],'window':{'from':start.isoformat(),'to':end.isoformat()},'records':len(records),'dataSHA256':hashlib.sha256(payload).hexdigest(),'collectorVersion':'kubernetes-log-snapshot/v1','coverageComplete':False,'authRef':'openbao://secret/data/ops/r37-victorialogs/source','endpoint':'https://127.0.0.1:19428','limitation':'One captured task Pod and closed half-open window; no continuous or cluster-wide log collection claim.'}
(work/'source-snapshot.json').write_text(json.dumps(receipt,indent=2));print(json.dumps({'status':'imported','records':len(records),'podUID':records[0]['uid'],'window':receipt['window']}))
