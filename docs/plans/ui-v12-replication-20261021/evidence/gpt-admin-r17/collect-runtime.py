"""Read only this newly introduced fixture's final state; never read credentials."""
from pathlib import Path
import json,subprocess
r=Path('/data/ops-ui-v12-mfa-20261022-r1/gpt-admin-r17')
log=(r/'integration-first.log').read_text()
assert log.rstrip().endswith('PASS'), 'no runtime acceptance before final PASS'
def sql(q):return subprocess.check_output(['docker','exec','ops-ui-v12-mfa-pg-20261022','psql','-U','postgres','-d','ui_v12_native_identity','-X','-tAc',q],text=True).strip()
assert sql("SELECT shobj_description(oid,'pg_database') FROM pg_database WHERE datname=current_database()")=='ops.task=ui-v12-p2-01-r1; isolated native identity only'
assert sql('SELECT max(version_id) FROM goose_db_version WHERE is_applied')=='57'
q="""SELECT json_build_object('tenantId',f.tenant_id,'raceTargetUserId',f.user_id,
'activeAdministrators',(SELECT count(*) FROM identity.accounts a JOIN identity.role_grants g USING(tenant_id,user_id) WHERE a.tenant_id=f.tenant_id AND a.status='active' AND g.role='platform_admin' AND g.status='active' AND g.authorization_version=a.authorization_version),
'committedDemotions',(SELECT count(*) FROM audit.records WHERE tenant_id=f.tenant_id AND event_type='identity.admin.update' AND record->'change'->>'status'='disabled'),
'raceCommittedDemotions',(SELECT json_agg(json_build_object('recordId',record_id,'requestId',record->>'requestId','targetUserId',entity_id,'actorSubject',subject)) FROM audit.records WHERE tenant_id=f.tenant_id AND event_type='identity.admin.update' AND record->'change'->>'status'='disabled' AND created_at>=f.created_at),
'rollbackRetryAudits',(SELECT json_agg(json_build_object('recordId',record_id,'requestId',record->>'requestId','targetUserId',entity_id,'actorSubject',subject,'event',event_type)) FROM audit.records WHERE tenant_id=f.tenant_id AND event_type='identity.admin.update' AND record->'change'->>'displayName'='Audit Rollback Fixture'),
'remainingTestTriggers',(SELECT count(*) FROM pg_trigger WHERE tgname LIKE 'ui_v12_admin_fault_%' AND NOT tgisinternal),
'remainingTestFunctions',(SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='audit' AND p.proname LIKE 'ui_v12_admin_fault_%'))
FROM identity.accounts f WHERE f.display_name='Concurrent Administrator Fixture' ORDER BY f.created_at DESC LIMIT 1"""
d=json.loads(sql(q));assert d['activeAdministrators']==1 and d['committedDemotions']==2
# One earlier managed-user disable/restore belongs to the same regression flow;
# the race itself adds exactly one successful demotion, as asserted by the test.
assert len(d['raceCommittedDemotions'])==1
assert len(d['rollbackRetryAudits'])==1 and d['remainingTestTriggers']==0 and d['remainingTestFunctions']==0
(r/'runtime-result.json').write_text(json.dumps(d,indent=2)+'\n')
print('read-only final fixture state and task-trigger cleanup verified')
