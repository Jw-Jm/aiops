"""Read only task rows and public UID/RV; do not export credentials or other resources."""
import json,os,socket,subprocess,time
from pathlib import Path
assert socket.gethostname()=='mssc'
root=Path('/data/ops-v12-native-visual-r24-20261026');work=root/'r35';ns='ops-native-v12-r24-20261026'
env=os.environ|{'PGPASSWORD':(root/'private/pg-password').read_text().strip()}
def query(sql):return json.loads(subprocess.check_output(['docker','exec','-e','PGPASSWORD','ops-v12-native-r24-pg','psql','-U','postgres','-d','ui_v12_r24_native','-tA','-c',sql],env=env))
deadline=time.monotonic()+120
while True:
 rows=query("SELECT COALESCE(json_agg(json_build_object('canonicalId',canonical_id,'namespace',namespace,'name',name,'metadata',metadata)), '[]'::json) FROM platform.resource_entities WHERE tenant_id='01a177f1-372f-75b7-be90-e8121d1445f5' AND name='r35-same-name' AND deleted_at IS NULL")
 if {r['namespace'] for r in rows}=={ns,'ops-native-v12-r35-scope'}:break
 assert time.monotonic()<deadline,'second namespace not persisted after repair'
 time.sleep(3)
proof={'status':'passed','sourceRevision':3,'rows':rows,'outsidePersisted':False,'physicalResources':[]}
for name in [ns,'ops-native-v12-r35-scope','ops-native-v12-r35-outside']:
 cm=json.loads(subprocess.check_output(['kubectl','get','cm','r35-same-name','-n',name,'-o','json']))
 proof['physicalResources'].append({'namespace':name,'uid':cm['metadata']['uid'],'resourceVersion':cm['metadata']['resourceVersion']})
proof['archiveVersions']=query("SELECT COALESCE(json_agg(t),'[]'::json) FROM (SELECT metadata->>'sourceRevision' AS revision,metadata->>'queryTemplateVersion' AS template,count(*) AS count FROM platform.evidence_metadata WHERE tenant_id='01a177f1-372f-75b7-be90-e8121d1445f5' AND source_id='01a17c3c-ef66-70cf-9ff1-cad25a8bee34' GROUP BY 1,2) t")
proof['evidence']=query("SELECT COALESCE(json_agg(t),'[]'::json) FROM (SELECT evidence_id,namespace,canonical_id,metadata->>'sourceRevision' AS revision,metadata->>'queryTemplateVersion' AS template,replay_state FROM platform.evidence_metadata WHERE tenant_id='01a177f1-372f-75b7-be90-e8121d1445f5' AND canonical_id IN (SELECT canonical_id FROM platform.resource_entities WHERE tenant_id='01a177f1-372f-75b7-be90-e8121d1445f5' AND name='r35-same-name') AND metadata->>'sourceRevision'='3')t")
(work/'namespace-persistence.json').write_text(json.dumps(proof,indent=2));print(json.dumps({'status':'passed','namespaces':[r['namespace'] for r in rows],'archiveVersions':proof['archiveVersions']}))
