import csv,datetime,json,socket,subprocess
from pathlib import Path
assert socket.gethostname()=='mssc'
root=Path('/data/ops-v12-native-visual-r24-20261026');r=root/'r45';ns='ops-native-v12-r24-20261026'
b=json.loads((r/'browser-controls.json').read_text());assert b['status']=='passed_scoped_comparison_and_page_export'
rows=list(csv.DictReader((r/'resources-page.csv').open(encoding='utf-8-sig',newline='')))
current={node['canonicalId']:node for node in b['export']['data']};initial={node['canonicalId'] for node in b['initial']['data']}
assert len(rows)==len(current)==b['exportRows']==5 and b['clickedMatchedTotal']==10
assert set(current)==initial=={row['规范资源标识'] for row in rows}
for row in rows:
 node=current[row['规范资源标识']]
 assert row['名称']==node['name'] and row['类型']==node['kind'] and row['健康状态']==node['attributes']['health'] and row['资源领域']==node['attributes']['resourceDomain']
 assert row['资源UID']==node['stableId'] and row['集群UID']==node['scope'] and row['命名空间']==ns
 assert row['本页导出数']=='5' and row['点击时匹配总数']=='10'
 assert row['请求ID']==b['export']['requestId'] and row['读取时间']==b['export']['meta']['collectedAt']
 assert row['部分结果']==str(b['export']['meta']['partial']).lower() and row['投影时效']==b['export']['meta']['freshness']
 assert row['OwnerEpoch']==str(b['export']['meta']['graphRevision']['ownerEpoch']) and row['GraphGeneration']==str(b['export']['meta']['graphRevision']['graphGeneration'])
 obj=json.loads(subprocess.check_output(['kubectl','-n',ns,'get',node['kind'],node['name'],'-o','json']))
 assert obj['metadata']['uid']==node['stableId']
ids=list(current)
sql="""BEGIN READ ONLY;
select json_build_object('canonicalId',canonical_id,'name',name,'kind',kind,'namespace',namespace,'deletedAt',deleted_at) from platform.resource_entities where canonical_id in (select jsonb_array_elements_text(:'ids'::jsonb));
select json_build_object('queryId',query_id,'resultDigest',result->>'resultDigest') from investigation.observation_queries where query_id='e630a865-c1c8-49f4-b3f5-5685ed117df4';
COMMIT;"""
out=subprocess.check_output(['docker','exec','-i','ops-v12-native-r24-pg','psql','-U','postgres','-d','ui_v12_r24_native','-At','-v','ON_ERROR_STOP=1','-v','ids='+json.dumps(ids)],input=sql,text=True)
records=[json.loads(line) for line in out.splitlines() if line.startswith('{')]
resourceRecords=[row for row in records if 'canonicalId' in row]
assert len(resourceRecords)==5
for row in resourceRecords:
 node=current[row['canonicalId']];assert row['deletedAt'] is None and row['name']==node['name'] and row['kind']==node['kind'] and row['namespace']==ns
log=next(row for row in records if 'queryId' in row);assert log['resultDigest']=='sha256:14f0d1e40f283dc68fcaebb0b71d82014d97aa1b6325b77a65af4f3387dd21dd'
previous=json.loads((root/'r44/backend-runtime.json').read_text())
for artifact in previous['artifacts']:
 pod=json.loads(subprocess.check_output(['kubectl','-n',ns,'get','pod',artifact['component'],'-o','json']))
 assert pod['metadata']['uid']==artifact['podUID'] and pod['spec']['containers'][0]['command'][0]==str(root/'r44'/artifact['artifact'])
receipt={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'status':'passed','transaction':'READ ONLY','exportRows':5,'clickedMatchedTotal':10,'identitiesMatchedCSVAPIKubernetesPG':5,'requestId':b['export']['requestId'],'graphRevision':b['export']['meta']['graphRevision'],'partial':b['export']['meta']['partial'],'coverageWarnings':b['export']['meta']['warnings'],'databaseRecords':resourceRecords,'savedLogDigestUnchanged':True,'backendR44PodsUnchanged':True,'limitations':['CSV contains only this page, not all matching resources','Coverage/source continuity warnings retained; no all-Kind health or full-source acceptance','No screenshots, natural TTL waits, model calls, business mutations or full-stack restart']}
(r/'export-oracle.json').write_text(json.dumps(receipt,indent=2));print('CSV/API/current Kubernetes/readonly PG: 5 exported identities, 10 clicked matches; backend r44 unchanged')
