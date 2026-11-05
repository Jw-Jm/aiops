"""Exercise the exact offline amd64 binary against a new network-isolated PostgreSQL (r55)."""
from pathlib import Path
import datetime,hashlib,json,os,secrets,socket,subprocess,time,urllib.parse
assert socket.gethostname()=='mssc'
w=Path('/data/ops-migration-renew-r55-20261105');private=w/'private';private.mkdir(mode=0o700)
name='ops-r55-migration-pg';image='ops.local/amd64-poc/postgresql-bookworm:17.11';binary=w/'amd64/db-migrate';migrations=w/'amd64/migrations'
opsctl=Path('/data/ops-migration-renew-r50-20261103/opsctl')
password=secrets.token_hex(32);loginpassword=secrets.token_hex(32)
pgfile=private/'postgres.env';pgfile.write_text('POSTGRES_PASSWORD='+password+'\nPOSTGRES_DB=r55_fresh\n');os.chmod(pgfile,0o600)
def call(args,data=None,check=True):
 p=subprocess.run(args,input=data,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,timeout=120)
 if check and p.returncode:raise RuntimeError('command failed: '+p.stdout.decode().replace(password,'[REDACTED]').replace(loginpassword,'[REDACTED]'))
 return p
report={'startedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'architecture':'linux/amd64','binarySHA256':'sha256:'+hashlib.sha256(binary.read_bytes()).hexdigest(),'postgresImage':image,'scope':'isolated test databases; no preview or production database access','cases':[]}
created=False
try:
 call(['docker','run','-d','--name',name,'--network','none','--pull=never','--label','ops.platform.owner=migration-renew-20261105','--memory','512m','--cpus','1','--env-file',str(pgfile),'--tmpfs','/var/lib/postgresql/data:rw,nosuid,nodev,size=512m',image]);created=True
 for i in range(30):
  if call(['docker','exec',name,'pg_isready','-U','postgres'],check=False).returncode==0:break
  time.sleep(1)
 def sql(db,text):
  return call(['docker','exec','-i',name,'psql','-U','postgres','-d',db,'-XAt','-v','ON_ERROR_STOP=1'],text.encode()).stdout.decode().strip()
 def run(db,target,restricted=True):
  user=db+'_migrator'if restricted else 'postgres';pwd=loginpassword if restricted else password
  q={'sslmode':'disable'}
  if restricted:q['options']='-c role=migration_role'
  dsn='postgresql://'+user+':'+pwd+'@127.0.0.1:5432/'+db+'?'+urllib.parse.urlencode(q,quote_via=urllib.parse.quote)
  env=private/'migration.env';env.write_text('SP03_MIGRATION_DATABASE_URL='+dsn+'\n');os.chmod(env,0o600)
  result=call(['docker','run','--rm','--pull=never','--read-only','--network=container:'+name,'--cap-drop','ALL','--security-opt','no-new-privileges','--env-file',str(env),'--mount',f'type=bind,source={binary},target=/db-migrate,readonly','--mount',f'type=bind,source={migrations},target=/migrations,readonly','--entrypoint','/db-migrate',image,'-dir','/migrations','-to',str(target)],check=False)
  out=result.stdout.decode();assert password not in out and loginpassword not in out
  (w/(db+'-'+str(target)+'-'+str(len(report['cases']))+'.log')).write_text(out)
  return result.returncode
 assert run('r55_fresh',1,False)==0
 def bootstrap(db):
  config={'schemaVersion':1,'migration':{'name':db+'_migrator','password':loginpassword},'api':{'name':db+'_api','password':secrets.token_hex(32)},'worker':{'name':db+'_worker','password':secrets.token_hex(32)}}
  configfile=private/'logins.json';configfile.write_text(json.dumps(config));os.chmod(configfile,0o600)
  envfile=private/'bootstrap.env';envfile.write_text('OPS_BOOTSTRAP_DATABASE_URL=postgresql://postgres:'+password+'@127.0.0.1:5432/'+db+'?sslmode=disable\n');os.chmod(envfile,0o600)
  call(['docker','run','--rm','--pull=never','--read-only','--network=container:'+name,'--env-file',str(envfile),'--mount',f'type=bind,source={opsctl},target=/opsctl,readonly','--mount',f'type=bind,source={private},target=/private,readonly','--entrypoint','/opsctl',image,'bootstrap','database-logins','--secrets-file','/private/logins.json'])
  assert sql(db,"SELECT has_database_privilege('schema_owner',current_database(),'CREATE') AND NOT has_database_privilege('api_runtime_role',current_database(),'CREATE') AND NOT has_database_privilege('worker_runtime_role',current_database(),'CREATE')")=='t'
 bootstrap('r55_fresh')
 assert sql('r55_fresh',"SELECT rolsuper OR rolbypassrls OR rolcreatedb OR rolcreaterole FROM pg_roles WHERE rolname='r55_fresh_migrator'")=='f'
 assert run('r55_fresh',0)==0
 version=lambda db:int(sql(db,'SELECT max(version_id) FROM public.goose_db_version WHERE is_applied'))
 assert version('r55_fresh')==74
 for target in [0,72]:assert run('r55_fresh',target)==0 and version('r55_fresh')==74
 assert run('r55_fresh',-1)!=0 and version('r55_fresh')==74
 report['cases'].append({'case':'fresh_1_to74_restricted_retry_lower_target_negative_refusal','passed':True,'version':74})
 sql('r55_fresh','CREATE DATABASE r55_upgrade;')
 assert run('r55_upgrade',1,False)==0
 bootstrap('r55_upgrade')
 assert run('r55_upgrade',72)==0
 seed="INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES('11111111-1111-4111-8111-111111111155','r55-retained','r55 retained tenant');"
 sql('r55_upgrade',seed)
 before=sql('r55_upgrade',"SELECT md5(row_to_json(t)::text) FROM platform.tenants t WHERE slug='r55-retained'")
 assert run('r55_upgrade',73)==0 and version('r55_upgrade')==73
 assert sql('r55_upgrade',"SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE oid='incident.followups'::regclass")=='t'
 assert sql('r55_upgrade',"SELECT count(*) FROM pg_policies WHERE schemaname='incident' AND tablename='followups' AND policyname='tenant_isolation'")=='1'
 definition=sql('r55_upgrade',"SELECT pg_get_functiondef('incident.transfer_eligible(uuid,text,text,text,text)'::regprocedure)")
 assert 'SECURITY DEFINER' in definition and 'identity.accounts' in definition and 'platform.sp05_lock_operator_scope' in definition
 assert sql('r55_upgrade',"SELECT has_function_privilege('api_runtime_role','incident.transfer_eligible(uuid,text,text,text,text)','EXECUTE') AND has_function_privilege('worker_runtime_role','incident.transfer_eligible(uuid,text,text,text,text)','EXECUTE')")=='t'
 assert sql('r55_upgrade',"SELECT count(*) FROM information_schema.columns WHERE table_schema='incident' AND table_name='records' AND column_name IN ('assignee_subject','assigned_at')")=='2'
 assert run('r55_upgrade',74)==0 and version('r55_upgrade')==74
 assert sql('r55_upgrade',"SELECT count(*) FROM pg_indexes WHERE schemaname='investigation' AND indexname='investigation_job_identity'")=='1'
 assert sql('r55_upgrade',"SELECT count(*) FROM information_schema.columns WHERE table_schema='investigation' AND table_name='jobs' AND column_name='admission_digest'")=='1'
 assert sql('r55_upgrade',"SELECT md5(row_to_json(t)::text) FROM platform.tenants t WHERE slug='r55-retained'")==before
 assert run('r55_upgrade',0)==0 and version('r55_upgrade')==74
 report['cases'].append({'case':'upgrade_72_to73_to74_retry_seed_unchanged_collab_rls_transfer_fn_admission_index','passed':True,'version':74,'retainedTenantDigest':before})
 state=json.loads(subprocess.check_output(['docker','inspect',name]))[0];assert state['HostConfig']['NetworkMode']=='none'
 report.update(status='passed',network='none',restrictedMigrationLogin=True,formalOpsctlBootstrap=True,createSchemaGrantedOnlyToOwner=True,negativeTargetRefused=True,nativeArm64SQLRun=False,completedAt=datetime.datetime.now(datetime.timezone.utc).isoformat())
finally:
 if created:call(['docker','rm','-f',name])
 report['testContainerRemoved']=created;(w/'forward-migration-amd64.json').write_text(json.dumps(report,indent=2))
print(json.dumps(report))
