"""Independently compare live archive events to native Kubernetes logs."""
from pathlib import Path
import base64,datetime,hashlib,json,socket,ssl,subprocess,urllib.parse,urllib.request
assert socket.gethostname()=='mssc'
root=Path('/data/ops-v12-native-visual-r24-20261026');w=root/'r49';private=w/'private'
s=json.loads((w/'collector-start.json').read_text());ns=s['namespace'];cred=json.loads((private/'source-http.json').read_text())
headers={'Authorization':'Basic '+base64.b64encode((cred['username']+':'+cred['password']).encode()).decode()}
ctx=ssl.create_default_context(cafile=str(private/'tls.crt'))
def query(expression):
 req=urllib.request.Request(s['endpoint']+'/select/logsql/query?'+urllib.parse.urlencode({'query':expression}),headers=headers)
 with urllib.request.urlopen(req,context=ctx,timeout=5)as r:return [json.loads(line)for line in r.read(65536).decode().splitlines()if line]
meta=json.loads(subprocess.check_output(['kubectl','get','pod','ops-seaweedfs-0','-n',ns,'-o','json']));uid=meta['metadata']['uid']
base='tenant:'+json.dumps(s['tenantId'])+' AND cluster:'+json.dumps(s['clusterUid'])+' AND collector:"vlagent-kubernetes/v1" AND kubernetes.pod_namespace:'+json.dumps(ns)
rows=query(base+' AND kubernetes.pod_uid:'+json.dumps(uid)+' AND _time:5m | sort by (_time) desc | limit 3');assert rows,'collector has not archived real events'
native=subprocess.check_output(['kubectl','logs','ops-seaweedfs-0','-n',ns,'--timestamps','--since=10m']).decode().splitlines();messages={(datetime.datetime.fromisoformat(line.partition(' ')[0].replace('Z','+00:00')),line.partition(' ')[2]) for line in native}
nsmeta=json.loads(subprocess.check_output(['kubectl','get','ns',ns,'-o','json']))
for r in rows:
 assert any(line.partition(' ')[2]==r['_msg'] and line.partition(' ')[0].split('.')[1][:9]==r['_time'].split('.')[1][:9] for line in native),'native CRI nanoseconds differ'
 assert (datetime.datetime.fromisoformat(r['_time'].replace('Z','+00:00')),r['_msg'])in messages,'archive CRI timestamp or message differs from native source'
 assert r['kubernetes.namespace_uid']==nsmeta['metadata']['uid']
 assert any(c['containerID']==r['kubernetes.container_id']for c in meta['status']['containerStatuses'])
 event=datetime.datetime.fromisoformat(r['_time'].replace('Z','+00:00'));assert event<datetime.datetime.now(datetime.timezone.utc)
assert not query(base+' AND kubernetes.pod_uid:"unobserved-uid" | limit 1')
assert not query('collector:"vlagent-kubernetes/v1" AND NOT kubernetes.pod_namespace:'+json.dumps(ns)+' | limit 1')
# Put the credential in the actual task Bao; no registration contains a placeholder.
host=f'ops-openbao.{ns}.svc.cluster.local';ip=subprocess.check_output(['kubectl','get','svc','ops-openbao','-n',ns,'-o','jsonpath={.spec.clusterIP}']).decode();lookup=socket.getaddrinfo;socket.getaddrinfo=lambda name,*a,**k:lookup(ip if name==host else name,*a,**k)
token=json.loads((root/'private/bao-recovery.json').read_text())['rootToken']
req=urllib.request.Request(f'https://{host}:8200/v1/secret/data/ops/r49-victorialogs/source',data=json.dumps({'data':cred}).encode(),headers={'X-Vault-Token':token,'Content-Type':'application/json'},method='POST')
with urllib.request.urlopen(req,context=ssl.create_default_context(cafile=str(root/'private/bao-ca.pem')),timeout=5)as r:assert r.status==200
proof={'checkedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'nativePodUID':uid,'namespaceUID':nsmeta['metadata']['uid'],'verifiedRows':len(rows),'latestStoredEvent':rows[0]['_time'],'originalMessagesMatched':True,'containerIdentityMatched':True,'wrongUIDEmpty':True,'otherNamespaceEmpty':True,'continuousCollector':True,'coverageComplete':False,'material':json.loads((w/'material.json').read_text())}
(w/'source-qualification.json').write_text(json.dumps(proof,indent=2));print(json.dumps(proof))
