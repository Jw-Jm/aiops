import datetime,hashlib,json,socket,subprocess
from pathlib import Path
assert socket.gethostname()=='mssc'
r=Path('/data/ops-v12-native-visual-r24-20261026/r47');ns='ops-native-v12-r24-20261026'
b=json.loads((r/'browser-filters.json').read_text());assert b['status']=='passed_scoped_resource_candidates_and_reset'
items=b['inventory']['data'];assert items
assert b['expected']['clusterScoped'] is False and b['expected']['namespaces']==[ns]
params={'ids':json.dumps([n['canonicalId'] for n in items])}
sql="""BEGIN READ ONLY;
select json_build_object('canonicalId',canonical_id,'namespace',namespace,'kind',kind,'name',name,'deletedAt',deleted_at) from platform.resource_entities where canonical_id in (select jsonb_array_elements_text(:'ids'::jsonb));
select json_build_object('queryId',query_id,'resultDigest',result->>'resultDigest') from investigation.observation_queries where query_id='e630a865-c1c8-49f4-b3f5-5685ed117df4';
select json_build_object('schema',max(version_id)) from goose_db_version where is_applied;
COMMIT;"""
args=['docker','exec','-i','ops-v12-native-r24-pg','psql','-U','postgres','-d','ui_v12_r24_native','-At','-v','ON_ERROR_STOP=1']
for key,value in params.items():args+=['-v',key+'='+value]
out=subprocess.check_output(args,input=sql,text=True);rows=[json.loads(line) for line in out.splitlines() if line.startswith('{')]
records={row['canonicalId']:row for row in rows if 'canonicalId' in row};assert len(records)==len(items)
for node in items:
 row=records[node['canonicalId']];assert row['deletedAt'] is None and (row['name'],row['kind'],row['namespace'])==(node['name'],node['kind'],node['namespace'])
assert next(row for row in rows if 'schema' in row)['schema']==71
assert next(row for row in rows if 'resultDigest' in row)['resultDigest']=='sha256:14f0d1e40f283dc68fcaebb0b71d82014d97aa1b6325b77a65af4f3387dd21dd'
current=json.loads(subprocess.check_output(['kubectl','-n',ns,'get','pods,services,statefulsets','-o','json']))['items'];kinds={'Pod','Service','StatefulSet'};verified=[]
for node in items:
 if node['kind'] not in kinds:continue
 actual=next(item for item in current if item['kind']==node['kind'] and item['metadata']['uid']==node['stableId'])
 assert actual['metadata']['name']==node['name'] and actual['metadata']['namespace']==node['namespace'];verified.append({'kind':node['kind'],'uid':node['stableId'],'name':node['name'],'namespace':node['namespace']})
expected=json.loads((r/'backend-runtime.json').read_text());components=[]
for artifact in expected['artifacts']:
 pod=json.loads(subprocess.check_output(['kubectl','-n',ns,'get','pod',artifact['component'],'-o','json']));command=pod['spec']['containers'][0]['command'][0]
 assert command==str(r/artifact['artifact']) and pod['metadata']['uid']==artifact['podUID'] and hashlib.sha256(Path(command).read_bytes()).hexdigest()==artifact['sha256'];components.append(artifact)
config=json.loads(subprocess.check_output(['kubectl','-n',ns,'get','cm','r28-runtime','-o','json']))['data'];sp06=json.loads(config['sp06.json']);assert sp06['assistant']['profile']['taskKinds']==['data_query'];sp04=json.loads(config['sp04.json']);assert not sp04.get('SP05',sp04.get('sp05'))
preview=json.loads(subprocess.check_output(['docker','inspect','ops-v12-native-r36b-web-30472']))[0];assert preview['State']['Running']
receipt={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'status':'passed_scoped_candidates_inventory_oracle','transaction':'READ ONLY','database':'ui_v12_r24_native','browserIndexSHA256':b['indexSHA256'],'inventoryRecords':len(items),'candidateKinds':b['expected']['kinds'],'candidateNamespaces':b['expected']['namespaces'],'clusterScopedCandidate':False,'rows':rows,'kubernetesIdentities':verified,'components':components,'modelProfileReference':sp06['assistant']['modelProfileVersion'],'modelTaskKinds':['data_query'],'SP05Enabled':False,'independent30472Running':True,'limitations':['Namespace-only operator; cluster-scoped positive and a second authorized namespace are covered only by unit fixtures','No full visual, source continuity, all-Kind status, two-cluster or capacity acceptance','API/Worker process replacement is not full-stack restart acceptance; preserved source configuration and old log digest do not renew expired source qualification']}
(r/'oracle-persistence.json').write_text(json.dumps(receipt,indent=2));print('scoped PG inventory, Kubernetes identities, runtime artifacts and unchanged gates verified')
