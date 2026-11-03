import datetime,json,socket,subprocess
from pathlib import Path
assert socket.gethostname()=='mssc'
base=Path('/data/ops-v12-native-visual-r24-20261026');work=base/'r46';ns='ops-native-v12-r24-20261026'
def kube(*args):return json.loads(subprocess.check_output(['kubectl',*args,'-o','json']))
def configuration(pod,variable):
 container=pod['spec']['containers'][0]
 path=next(item['value'] for item in container['env'] if item['name']==variable)
 mount=max((item for item in container['volumeMounts'] if path==item['mountPath'] or path.startswith(item['mountPath']+'/')),key=lambda item:len(item['mountPath']))
 volume=next(item for item in pod['spec']['volumes'] if item['name']==mount['name'])
 assert 'configMap' in volume
 configmap=volume['configMap']['name'];document=kube('-n',ns,'get','cm',configmap)['data']
 key=mount.get('subPath') or path[len(mount['mountPath']):].lstrip('/')
 return json.loads(document[key]),{'variable':variable,'runtimeFile':path,'configMap':configmap}
namespace=kube('get','namespace',ns);assert namespace['metadata']['uid']=='731f0e88-1893-4836-844b-a29374c1ca11'
browser=json.loads((work/'browser-detail.json').read_text());assert browser['status'] in ['passed_scoped_resource_detail','passed_resource_detail_subset_events_runtime_blocked']
node=browser['entity'];pod=kube('-n',ns,'get','pod',node['name']);assert pod['metadata']['uid']==node['stableId'] and pod['metadata']['namespace']==node['namespace'] and node['scope']=='79014400-9453-4cb4-9678-0fae47981d79'
expected=json.loads((base/'r44/backend-runtime.json').read_text());components=[]
for artifact in expected['artifacts']:
 actual=kube('-n',ns,'get','pod',artifact['component']);assert actual['metadata']['uid']==artifact['podUID']
 config,origin=configuration(actual,'SP04_RUNTIME_FILE');sp05=config.get('SP05',config.get('sp05'));enabled=bool(sp05 and sp05.get('Enabled',sp05.get('enabled',False)))
 components.append({'component':artifact['component'],'podUID':actual['metadata']['uid'],'runtime':origin,'SP05Present':sp05 is not None,'SP05Enabled':enabled})
assert all(not component['SP05Enabled'] for component in components)
source_ids=[source['sourceRegistrationId'] for source in browser['resource']['data']['observationSources']]
params={'canonical':node['canonicalId'],'tenant':node['tenantId'],'sources':json.dumps(source_ids)}
sql="""BEGIN READ ONLY;
select json_build_object('canonicalId',canonical_id,'name',name,'kind',kind,'namespace',namespace,'deletedAt',deleted_at) from platform.resource_entities where tenant_id=:'tenant' and canonical_id=:'canonical';
select json_build_object('sourceId',source_id,'revision',revision,'status',status) from platform.source_registrations where tenant_id=:'tenant' and source_id::text in (select jsonb_array_elements_text(:'sources'::jsonb));
select json_build_object('findingRecords',count(*)) from finding.records where tenant_id=:'tenant' and resource_canonical_id=:'canonical';
select json_build_object('incidentRecords',count(*)) from incident.records where tenant_id=:'tenant' and resource_canonical_id=:'canonical';
select json_build_object('schema',max(version_id)) from goose_db_version where is_applied;
select json_build_object('queryId',query_id,'resultDigest',result->>'resultDigest') from investigation.observation_queries where query_id='e630a865-c1c8-49f4-b3f5-5685ed117df4';
COMMIT;"""
args=['docker','exec','-i','ops-v12-native-r24-pg','psql','-U','postgres','-d','ui_v12_r24_native','-At','-v','ON_ERROR_STOP=1']
for key,value in params.items():args+=['-v',key+'='+value]
out=subprocess.check_output(args,input=sql,text=True);rows=[json.loads(line) for line in out.splitlines() if line.startswith('{')]
record=next(row for row in rows if 'canonicalId' in row);assert record['canonicalId']==node['canonicalId'] and record['deletedAt'] is None and record['name']==node['name']
for source in browser['resource']['data']['observationSources']:
 row=next(row for row in rows if row.get('sourceId')==source['sourceRegistrationId']);assert row['status']=='active' and row['revision']==source['revision']
assert next(row for row in rows if 'schema' in row)['schema']==71
assert next(row for row in rows if 'resultDigest' in row)['resultDigest']=='sha256:14f0d1e40f283dc68fcaebb0b71d82014d97aa1b6325b77a65af4f3387dd21dd'
receipt={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'status':'passed_scoped_identity_source_runtime_oracle','transaction':'READ ONLY','database':'ui_v12_r24_native','browserIndexSHA256':browser['indexSHA256'],'schema':71,'kubernetesIdentity':{'uid':pod['metadata']['uid'],'resourceVersion':pod['metadata']['resourceVersion'],'name':pod['metadata']['name'],'namespace':pod['metadata']['namespace']},'rows':rows,'components':components,'eventRuntime':'SP05 not enabled in both API/Worker SP04 configuration; actual public read409 CAPABILITY_DISABLED; DB counts do not replace the gated API','limits':['resource identity and current source registration revision only; no continuity or all-source qualification claim','no real nonempty SP05 event read; no enabling SP05 or model task expansion','no migration, model call, full restart, long load or natural saved-result TTL acceptance']}
(work/'runtime-oracle.json').write_text(json.dumps(receipt,indent=2));print('readonly resource/source version checks passed; actual SP05 API+Worker disabled configuration confirmed')
