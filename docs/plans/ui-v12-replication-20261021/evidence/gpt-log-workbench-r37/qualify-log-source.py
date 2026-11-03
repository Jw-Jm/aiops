"""Measure real bounded source templates before supplying runtime qualification."""
from pathlib import Path
import base64,datetime,hashlib,json,socket,ssl,urllib.request,urllib.parse
assert socket.gethostname()=='mssc'
work=Path('/data/ops-v12-native-visual-r24-20261026/r37');snapshot=json.loads((work/'source-snapshot.json').read_text());registration=json.loads((work/'source-registration.json').read_text())['outcome'];private=work/'private'
credential=json.loads((private/'source-http.json').read_text());headers={'Authorization':'Basic '+base64.b64encode((credential['username']+':'+credential['password']).encode()).decode(),'Content-Type':'application/x-www-form-urlencoded'};ctx=ssl.create_default_context(cafile=str(private/'tls.crt'))
start=datetime.datetime.fromisoformat(snapshot['window']['from']).astimezone(datetime.timezone.utc).isoformat();end=datetime.datetime.fromisoformat(snapshot['window']['to']).astimezone(datetime.timezone.utc).isoformat()
base=' AND '.join(key+':'+json.dumps(value) for key,value in [('tenant',snapshot['tenantId']),('cluster',snapshot['cluster']['clusterUid']),('namespace',snapshot['namespace']),('uid',snapshot['podUID'])])+f' AND _time:[{start},{end})'
def query(path,expression):
 request=urllib.request.Request(snapshot['endpoint']+path,data=urllib.parse.urlencode({'query':expression,'time':end,'start':start,'end':end,'allow_partial_response':'0'}).encode(),headers=headers)
 with urllib.request.urlopen(request,context=ctx,timeout=5) as reply:assert reply.status==200;return reply.read()
aggregate=json.loads(query('/select/logsql/stats_query',base+' | stats count() as value'))
assert aggregate['status']=='success' and not aggregate.get('isPartial') and not aggregate.get('warnings')
rows=aggregate['data']['result'];count=sum(float(r['value'][1]) for r in rows);assert count==snapshot['records']
samples=[json.loads(line) for line in query('/select/logsql/query',base+' | sort by (_time) desc | limit 21').decode().splitlines() if line]
assert 0<len(samples)<=21
for item in samples:
 assert item['tenant']==snapshot['tenantId'] and item['cluster']==snapshot['cluster']['clusterUid'] and item['namespace']==snapshot['namespace'] and item['uid']==snapshot['podUID']
 assert datetime.datetime.fromisoformat(start)<=datetime.datetime.fromisoformat(item['_time'].replace('Z','+00:00'))<datetime.datetime.fromisoformat(end)
negative=[]
for key,value in [('tenant','unregistered-tenant'),('namespace','unregistered-namespace'),('uid','unobserved-resource-uid')]:
 expression=base.replace(key+':'+json.dumps(dict(tenant=snapshot['tenantId'],namespace=snapshot['namespace'],uid=snapshot['podUID'])[key]),key+':'+json.dumps(value))+' | stats count() as value'
 result=json.loads(query('/select/logsql/stats_query',expression));assert sum(float(r['value'][1]) for r in result['data']['result'])==0;negative.append(key)
proof={'schemaVersion':'bounded-log-snapshot-qualification/v1','sourceId':registration['sourceId'],'sourceRevision':registration['revision'],'nativeSnapshot':snapshot,'populationMeasured':int(count),'sampleRowsVerified':len(samples),'negativeIdentitySelectors':negative,'templateVersions':['logs.records/v1','logs.records-samples/v1'],'completeCollectionCoverage':False,'continuousCollector':False,'checkedAt':datetime.datetime.now(datetime.timezone.utc).isoformat()}
(work/'source-qualification.json').write_text(json.dumps(proof,indent=2));print(json.dumps({'status':'passed','populationMeasured':int(count),'sampleRowsVerified':len(samples),'negativeIdentitySelectors':negative,'completeCollectionCoverage':False}))
