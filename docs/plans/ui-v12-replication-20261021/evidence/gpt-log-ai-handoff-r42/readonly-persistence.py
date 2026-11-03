import json,subprocess,datetime
from pathlib import Path
root=Path('/data/ops-v12-native-visual-r24-20261026/r42')
sql="""BEGIN READ ONLY;
select json_build_object('queryId',query_id,'resultDigest',result->>'resultDigest','state',state) from investigation.observation_queries where query_id='e630a865-c1c8-49f4-b3f5-5685ed117df4';
select json_build_object('newR42Conversations',count(*)) from investigation.assistant_conversations where title like 'r42 %';
COMMIT;"""
out=subprocess.check_output(['docker','exec','-i','ops-v12-native-r24-pg','psql','-U','postgres','-d','ui_v12_r24_native','-At','-v','ON_ERROR_STOP=1'],input=sql,text=True)
rows=[json.loads(line) for line in out.splitlines() if line.startswith('{')]
assert rows[0]['resultDigest']=='sha256:14f0d1e40f283dc68fcaebb0b71d82014d97aa1b6325b77a65af4f3387dd21dd'
assert rows[1]['newR42Conversations']==0
(root/'readonly-persistence.json').write_text(json.dumps({'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'database':'ui_v12_r24_native','transaction':'READ ONLY','rows':rows,'scope':'original immutable log result unchanged; model gate prevents a new r42 conversation; no successful remote creation/persistence claimed'},indent=2))
print('readonly persistence checks passed')
