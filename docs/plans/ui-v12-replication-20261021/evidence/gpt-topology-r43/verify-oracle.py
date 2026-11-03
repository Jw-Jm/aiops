import datetime,json,socket,subprocess
from pathlib import Path
assert socket.gethostname()=='mssc'
r=Path('/data/ops-v12-native-visual-r24-20261026/r43');ns='ops-native-v12-r24-20261026'
b=json.loads((r/'browser-topology.json').read_text());g=b['depth2']['data'];db=json.loads((r/'readonly-resources.json').read_text())
assert b['status']=='passed_scoped_topology'
rows={row['canonicalId']:row for row in db['resources']};objects={};oracle=[]
for node in g['nodes']:
 assert node['namespace']==ns
 obj=json.loads(subprocess.check_output(['kubectl','-n',ns,'get',node['kind'],node['name'],'-o','json']))
 assert obj['metadata']['uid']==node['stableId']
 row=rows[node['canonicalId']];assert row['deletedAt'] is None and row['name']==node['name'] and row['namespace']==node['namespace'] and row['kind']==node['kind']
 objects[node['canonicalId']]=obj
 # Keep identity/relationship fields only; never export ConfigMap data or container env values.
 oracle.append({'canonicalId':node['canonicalId'],'uid':obj['metadata']['uid'],'resourceVersion':obj['metadata']['resourceVersion'],'kind':obj['kind'],'namespace':obj['metadata']['namespace'],'name':obj['metadata']['name'],'ownerReferences':obj['metadata'].get('ownerReferences',[]),'labels':obj['metadata'].get('labels',{}),'selector':obj.get('spec',{}).get('selector'),'configMapVolumes':[volume['configMap']['name'] for volume in obj.get('spec',{}).get('volumes',[]) if 'configMap' in volume]})
checked=[]
for edge in g['edges']:
 source,target=objects[edge['from']],objects[edge['to']]
 if edge['kind']=='managed_by':valid=any(ref['uid']==target['metadata']['uid'] for ref in source['metadata'].get('ownerReferences',[]))
 elif edge['kind']=='selects_pod':
  selector=source['spec']['selector'];valid=bool(selector) and all(target['metadata']['labels'].get(key)==value for key,value in selector.items())
 elif edge['kind']=='related_to' and target['kind']=='ConfigMap':valid=any(volume.get('configMap',{}).get('name')==target['metadata']['name'] for volume in source['spec'].get('volumes',[]))
 else:raise AssertionError('oracle missing for actual relation kind '+edge['kind'])
 assert valid
 checked.append({'from':edge['from'],'to':edge['to'],'kind':edge['kind'],'verifiedAgainst':'Kubernetes UID/ownerReference/selector/volume fields; independently read from API'})
assert db['savedLogResult'][0]['resultDigest']=='sha256:14f0d1e40f283dc68fcaebb0b71d82014d97aa1b6325b77a65af4f3387dd21dd'
# Each runtime actually mounts this same frozen model material; inspect only public config paths.
components=[]
for name in ['ops-api','ops-worker','ops-investigator']:
 pod=json.loads(subprocess.check_output(['kubectl','-n',ns,'get','pod',name,'-o','json']))
 volumes={volume['name']:volume for volume in pod['spec']['volumes']};container=pod['spec']['containers'][0]
 mount=next(mount for mount in container['volumeMounts'] if mount['mountPath']=='/etc/r28');assert volumes[mount['name']]['configMap']['name']=='r28-runtime'
 if name!='ops-investigator':assert next(env['value'] for env in container['env'] if env['name']=='SP06_RUNTIME_FILE')=='/etc/r28/sp06.json'
 components.append({'component':name,'podUID':pod['metadata']['uid'],'modelMaterialMount':'r28-runtime:/etc/r28','runtimeFile':'sp06.json' if name!='ops-investigator' else 'investigator.json'})
receipt={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'status':'passed','databaseTransaction':'READ ONLY','verifiedNodes':len(oracle),'verifiedEdges':len(checked),'oracle':oracle,'edges':checked,'components':components,'savedLogDigestUnchanged':True,'limitations':['No Worker/full-stack restart this round','Current resource rows and live Kubernetes objects; not historical topology reconstruction or source continuity proof','UI storage is URL parameters; no new business mutation or migration']}
(r/'oracle-persistence.json').write_text(json.dumps(receipt,indent=2));print(json.dumps({'verifiedNodes':len(oracle),'verifiedEdges':len(checked),'components':len(components)}))
