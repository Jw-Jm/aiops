import datetime,hashlib,json,socket,subprocess
from pathlib import Path
assert socket.gethostname()=='mssc'
r=Path('/data/ops-v12-native-visual-r24-20261026/r44');ns='ops-native-v12-r24-20261026'
b=json.loads((r/'browser-catalog.json').read_text());assert b['status']=='passed_scoped_catalog_search_health_quickview'
node=b['exact']['data'][0]
pod=json.loads(subprocess.check_output(['kubectl','-n',ns,'get','pod','ops-seaweedfs-0','-o','json']))
assert pod['metadata']['uid']==node['stableId'] and node['namespace']==ns and pod['kind']==node['kind']=='Pod'
assert pod['status']['phase']=='Running' and any(c['type']=='Ready' and c['status']=='True' for c in pod['status']['conditions'])
assert node['attributes']['health']=='normal' and node['attributes']['resourceDomain']=='kubernetes'
assert b['searchRequest']['meta']['summary']['total']==len(b['searchRequest']['data'])==1
uid=node['stableId'];assert len(uid)==36 and set(uid.lower())<=set('abcdef0123456789-')
sql=f"""BEGIN READ ONLY;
select json_build_object('canonicalId',canonical_id,'namespace',namespace,'kind',kind,'name',name,'status',metadata->'status','observedAt',observed_at,'deletedAt',deleted_at) from platform.resource_entities where canonical_id='{node['canonicalId']}';
select json_build_object('queryId',query_id,'resultDigest',result->>'resultDigest','state',state) from investigation.observation_queries where query_id='e630a865-c1c8-49f4-b3f5-5685ed117df4';
select json_build_object('schema',max(version_id)) from goose_db_version;
COMMIT;"""
out=subprocess.check_output(['docker','exec','-i','ops-v12-native-r24-pg','psql','-U','postgres','-d','ui_v12_r24_native','-At','-v','ON_ERROR_STOP=1'],input=sql,text=True)
rows=[json.loads(line) for line in out.splitlines() if line.startswith('{')]
assert rows[0]['canonicalId']==node['canonicalId'] and rows[0]['deletedAt'] is None
assert rows[1]['resultDigest']=='sha256:14f0d1e40f283dc68fcaebb0b71d82014d97aa1b6325b77a65af4f3387dd21dd' and rows[2]['schema']==71
old=json.loads((r/'backend-runtime.json').read_text());components=[]
for expected in old['artifacts']:
 poddoc=json.loads(subprocess.check_output(['kubectl','-n',ns,'get','pod',expected['component'],'-o','json']))
 command=poddoc['spec']['containers'][0]['command'][0];assert command==str(r/expected['artifact'])
 assert hashlib.sha256(Path(command).read_bytes()).hexdigest()==expected['sha256'];assert poddoc['metadata']['uid']==expected['podUID']
 components.append({'component':expected['component'],'artifact':command,'sha256':expected['sha256'],'podUID':expected['podUID']})
sp06=json.loads(json.loads(subprocess.check_output(['kubectl','-n',ns,'get','cm','r28-runtime','-o','json']))['data']['sp06.json']);assert sp06['assistant']['profile']['taskKinds']==['data_query']
preview=json.loads(subprocess.check_output(['docker','inspect','ops-v12-native-r36b-web-30472']))[0];assert preview['State']['Running']
receipt={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'status':'passed','transaction':'READ ONLY','database':'ui_v12_r24_native','rows':rows,'kubernetesOracle':{'uid':pod['metadata']['uid'],'resourceVersion':pod['metadata']['resourceVersion'],'kind':pod['kind'],'namespace':pod['metadata']['namespace'],'name':pod['metadata']['name'],'phase':pod['status']['phase'],'ready':True},'components':components,'modelProfileReference':sp06['assistant']['modelProfileVersion'],'modelTaskKindsUnchanged':True,'independent30472Running':True,'limitations':['Actual UID query/normal Pod health; not every resource Kind health classification acceptance','No model call, business mutation, natural TTL wait, screenshot, long load, migration or full stack restart this round','Read success after process replacement does not prove graph source continuity or eliminate startup503']}
(r/'oracle-persistence.json').write_text(json.dumps(receipt,indent=2));print('independent Kubernetes, readonly PG and deployed artifact checks passed')
