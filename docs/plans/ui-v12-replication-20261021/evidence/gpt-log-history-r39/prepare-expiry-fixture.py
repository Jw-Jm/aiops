"""Controlled metadata-only expiry fixture; never modifies an existing query or result."""
from pathlib import Path
import json,uuid,subprocess,socket,datetime
assert socket.gethostname()=='mssc'
w=Path('/data/ops-v12-native-visual-r24-20261026/r39');assert not (w/'expiry-fixture.json').exists()
r=json.loads((w/'browser-history.json').read_text());assert r['status']=='passed'
base=str(uuid.UUID(r['outcome']['queryId']));expired=str(uuid.uuid4());key=str(uuid.uuid4())
sql=f"""INSERT INTO investigation.observation_queries(tenant_id,query_id,owner_subject,request_key,cluster_uid,session_id,request,scope,view,result,state,revision,created_at,updated_at,expires_at)
SELECT tenant_id,'{expired}'::uuid,owner_subject,'{key}'::uuid,cluster_uid,session_id,request,scope,
view || jsonb_build_object('queryId','{expired}','state','expired','request',request,'resultRef',NULL,'cancelRequestedAt',NULL,'termination',NULL,'failure',jsonb_build_object('code','RESULT_EXPIRED','retryable',false,'message','Controlled expiry contract fixture'),'createdAt',clock_timestamp()-interval '31 days','updatedAt',clock_timestamp()-interval '31 days','expiresAt',clock_timestamp()-interval '1 minute'),
NULL,'expired',revision,clock_timestamp()-interval '31 days',clock_timestamp()-interval '31 days',clock_timestamp()-interval '1 minute'
FROM investigation.observation_queries WHERE query_id='{base}'::uuid AND state='completed' RETURNING query_id;"""
out=subprocess.check_output(['docker','exec','-i','ops-v12-native-r24-pg','psql','-U','postgres','-d','ui_v12_r24_native','-At','-v','ON_ERROR_STOP=1'],input=sql,text=True);assert expired in out
receipt={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'kind':'controlled metadata-only expiry fixture; no aged live result or source execution claimed','queryId':expired,'baseQueryId':base,'database':'ui_v12_r24_native','resultStored':False,'existingRowsChanged':False}
(w/'expiry-fixture.json').write_text(json.dumps(receipt,indent=2));print(json.dumps(receipt))
