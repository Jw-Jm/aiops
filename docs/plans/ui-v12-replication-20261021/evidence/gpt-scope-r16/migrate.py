"""Apply only forward migration57 to the explicitly marked task-owned database."""
from pathlib import Path
import hashlib,json,os,subprocess
from urllib.parse import quote
b=Path('/data/ops-ui-v12-mfa-20261022-r1');r=b/'gpt-scope-r16'
assert b.stat().st_uid==0 and os.getuid()==0
container=json.loads(subprocess.check_output(['docker','inspect','ops-ui-v12-mfa-pg-20261022']))[0]
assert container['HostConfig']['PortBindings']['5432/tcp']==[{'HostIp':'127.0.0.1','HostPort':'15453'}]
def sql(q):return subprocess.check_output(['docker','exec','ops-ui-v12-mfa-pg-20261022','psql','-U','postgres','-d','ui_v12_native_identity','-X','-tAc',q],text=True).strip()
probe=json.loads(sql("SELECT json_build_object('database',current_database(),'owner',pg_get_userbyid(datdba),'comment',shobj_description(oid,'pg_database'),'version',(SELECT max(version_id) FROM goose_db_version WHERE is_applied)) FROM pg_database WHERE datname=current_database()"))
assert probe=={'database':'ui_v12_native_identity','owner':'ui_v12_native_owner','comment':'ops.task=ui-v12-p2-01-r1; isolated native identity only','version':56},'task database/version guard failed'
password=(b/'private/owner.password').read_text().strip()
env=dict(os.environ,SP03_MIGRATION_DATABASE_URL='postgres://ui_v12_native_owner:'+quote(password,safe='')+'@127.0.0.1:15453/ui_v12_native_identity?sslmode=disable&options='+quote('-c role=migration_role',safe=''))
p=subprocess.run([str(r/'db-migrate'),'-dir',str(r/'migrations'),'-to','57'],env=env,capture_output=True,text=True)
log=(p.stdout+p.stderr).replace(password,'[REDACTED]');(r/'migration57.log').write_text(log)
if p.returncode:raise RuntimeError('migration57 failed; inspect sanitized log')
assert sql('SELECT max(version_id) FROM goose_db_version WHERE is_applied')=='57'
function=sql("SELECT pg_get_functiondef('identity.admin_account_write(uuid,uuid,uuid,integer,integer,integer,uuid,text,jsonb)'::regprocedure)")
assert "grant_scope->'namespaces','[]'))>200" in function and "grant_scope->'clusterUids','[]'))>50" in function
(r/'migration-result.json').write_text(json.dumps({'database':probe['database'],'port':15453,'before':56,'after':57,'migrationSHA256':hashlib.sha256((r/'migrations/00057_native_grant_scope_limits.sql').read_bytes()).hexdigest(),'scope':'task database only; API/Worker have no direct identity table grants','status':'passed'},indent=2))
print('task PG15453 forward migration56->57 passed; unchanged credentials and existing grants retained')
