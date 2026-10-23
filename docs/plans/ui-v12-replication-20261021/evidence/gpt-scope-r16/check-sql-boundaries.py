"""Exercise security-definer scope bounds as API role; all probe writes roll back."""
from pathlib import Path
import json,subprocess
from uuid import UUID
b=Path('/data/ops-ui-v12-mfa-20261022-r1');r=b/'gpt-scope-r16';p=b/'gpt-login-r15/preview-r2'
tenant=str(UUID((p/'private/tenant-id').read_text().strip()))
user=str(UUID(json.loads((p/'private/browser-created-account.json').read_text())['userId']))
login=json.loads((p/'private/browser-identity.json').read_text())['loginName']
def quote(s):return "'"+s.replace("'","''")+"'"
def sql(q):return subprocess.check_output(['docker','exec','-i','ops-ui-v12-mfa-pg-20261022','psql','-U','postgres','-d','ui_v12_native_identity','-X','-v','ON_ERROR_STOP=1','-tAc',q],text=True).strip()
assert sql("SELECT shobj_description(oid,'pg_database') FROM pg_database WHERE datname=current_database()")=='ops.task=ui-v12-p2-01-r1; isolated native identity only'
assert sql('SELECT max(version_id) FROM goose_db_version WHERE is_applied')=='57'
actor=json.loads(sql(f"SELECT json_build_object('user',a.user_id,'session',s.session_id,'cv',a.credential_version,'av',a.authorization_version,'sv',a.session_version) FROM identity.accounts a JOIN identity.sessions s USING(tenant_id,user_id) WHERE a.tenant_id='{tenant}' AND a.login_name={quote(login)} AND s.revoked_at IS NULL AND s.mode='full' AND s.idle_expires_at>clock_timestamp() AND s.issued_credential_version=a.credential_version AND s.issued_authorization_version=a.authorization_version ORDER BY s.issued_at DESC LIMIT 1"))
actor['user']=str(UUID(actor['user']));actor['session']=str(UUID(actor['session']))
query=f"SELECT json_build_object('av',a.authorization_version,'sv',a.session_version,'revision',a.account_revision,'scope',g.scope,'binding',g.binding_id) FROM identity.accounts a JOIN identity.role_grants g USING(tenant_id,user_id) WHERE a.tenant_id='{tenant}' AND a.user_id='{user}' AND g.role='operator' AND g.status='active' AND g.authorization_version=a.authorization_version"
before=json.loads(sql(query));assert len(before['scope']['namespaces'])==200 and len(before['scope']['clusterUids'])==1
args=','.join([quote(tenant),quote(actor['user']),quote(actor['session']),str(int(actor['cv'])),str(int(actor['av'])),str(int(actor['sv'])),quote(user),quote('grant')])
def call(scope):
 body=json.dumps({'role':'operator','scope':scope,'expectedAuthorizationVersion':before['av'],'reason':'isolated rollback-only SQL scope boundary probe'})
 return 'identity.admin_account_write('+args+','+quote(body)+'::jsonb)'
invalid=[{'clusterUids':before['scope']['clusterUids'],'namespaces':['ns-'+str(i) for i in range(201)]},{'clusterUids':['invalid-cluster-'+str(i) for i in range(51)],'namespaces':[]}]
q=f"BEGIN; SET LOCAL ROLE api_runtime_role; SELECT set_config('app.tenant_id','{tenant}',true);\n"
for scope in invalid:
 q+="DO $$ DECLARE rejected boolean:=false; BEGIN BEGIN PERFORM "+call(scope)+"; EXCEPTION WHEN SQLSTATE '22023' THEN IF SQLERRM<>'invalid native grant scope' THEN RAISE; END IF; rejected:=true; END; IF NOT rejected THEN RAISE EXCEPTION 'scope over limit accepted'; END IF; END $$;\n"
q+="DO $$ DECLARE v jsonb; BEGIN v:="+call(before['scope'])+"; IF jsonb_array_length(v->'scope'->'namespaces')<>200 THEN RAISE EXCEPTION 'maximum scope lost'; END IF; END $$; ROLLBACK;"
subprocess.run(['docker','exec','-i','ops-ui-v12-mfa-pg-20261022','psql','-U','postgres','-d','ui_v12_native_identity','-X','-v','ON_ERROR_STOP=1'],input=q,text=True,capture_output=True,check=True)
after=json.loads(sql(query));assert before==after,'rollback probe changed persisted grant/versions'
(r/'sql-boundaries.json').write_text(json.dumps({'status':'passed','schemaVersion':57,'role':'api_runtime_role','namespaces200':'accepted inside rolled-back transaction','namespaces201':'SQL22023 invalid native grant scope','clusters51':'SQL22023 invalid native grant scope before registry check','rollback':'persisted binding/scope/account versions unchanged','persistedNamespaceCount':200},indent=2))
print('API-role SQL probes passed:200 accepted,201 namespaces/51 clusters rejected; probe writes rolled back')
