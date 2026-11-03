"""Exercise the exact offline amd64 binary against a new network-isolated PostgreSQL."""
from pathlib import Path
import datetime,hashlib,json,os,secrets,socket,subprocess,time,urllib.parse
assert socket.gethostname()=='mssc'
w=Path('/data/ops-migration-renew-r50-20261103');private=w/'private-v2';private.mkdir(mode=0o700,exist_ok=False)
name='ops-r50-migration-pg';image='ops.local/amd64-poc/postgresql-bookworm:17.11';binary=w/'amd64/db-migrate';migrations=w/'amd64/migrations'
password=secrets.token_hex(32);loginpassword=secrets.token_hex(32)
pgfile=private/'postgres.env';pgfile.write_text('POSTGRES_PASSWORD='+password+'\nPOSTGRES_DB=r50_fresh\n');os.chmod(pgfile,0o600)
def call(args,data=None,check=True):
 p=subprocess.run(args,input=data,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,timeout=90)
 if check and p.returncode:raise RuntimeError('command failed: '+p.stdout.decode().replace(password,'[REDACTED]').replace(loginpassword,'[REDACTED]'))
 return p
report={'startedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'architecture':'linux/amd64','binarySHA256':'sha256:'+hashlib.sha256(binary.read_bytes()).hexdigest(),'postgresImage':image,'scope':'isolated test databases; no preview or production database access','cases':[]}
created=False
try:
 call(['docker','run','-d','--name',name,'--network','none','--pull=never','--label','ops.platform.owner=migration-renew-20261103','--memory','512m','--cpus','1','--env-file',str(pgfile),'--tmpfs','/var/lib/postgresql/data:rw,nosuid,nodev,size=512m',image]);created=True
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
 assert run('r50_fresh',1,False)==0
 def bootstrap(db):
  config={'schemaVersion':1,'migration':{'name':db+'_migrator','password':loginpassword},'api':{'name':db+'_api','password':secrets.token_hex(32)},'worker':{'name':db+'_worker','password':secrets.token_hex(32)}}
  configfile=private/'logins.json';configfile.write_text(json.dumps(config));os.chmod(configfile,0o600)
  envfile=private/'bootstrap.env';envfile.write_text('OPS_BOOTSTRAP_DATABASE_URL=postgresql://postgres:'+password+'@127.0.0.1:5432/'+db+'?sslmode=disable\n');os.chmod(envfile,0o600)
  call(['docker','run','--rm','--pull=never','--read-only','--network=container:'+name,'--env-file',str(envfile),'--mount',f'type=bind,source={w/"opsctl"},target=/opsctl,readonly','--mount',f'type=bind,source={private},target=/private,readonly','--entrypoint','/opsctl',image,'bootstrap','database-logins','--secrets-file','/private/logins.json'])
  assert sql(db,"SELECT has_database_privilege('schema_owner',current_database(),'CREATE') AND NOT has_database_privilege('api_runtime_role',current_database(),'CREATE') AND NOT has_database_privilege('worker_runtime_role',current_database(),'CREATE')")=='t'
 bootstrap('r50_fresh')
 assert sql('r50_fresh',"SELECT rolsuper OR rolbypassrls OR rolcreatedb OR rolcreaterole FROM pg_roles WHERE rolname='r50_fresh_migrator'")=='f'
 assert run('r50_fresh',0)==0
 version=lambda db:int(sql(db,'SELECT max(version_id) FROM public.goose_db_version WHERE is_applied'))
 assert version('r50_fresh')==72
 for target in [0,70]:assert run('r50_fresh',target)==0 and version('r50_fresh')==72
 assert run('r50_fresh',-1)!=0 and version('r50_fresh')==72
 report['cases'].append({'case':'fresh_1_to72_restricted_retry_lower_target_negative_refusal','passed':True,'version':72})
 sql('r50_fresh','CREATE DATABASE r50_upgrade;')
 assert run('r50_upgrade',1,False)==0
 bootstrap('r50_upgrade')
 assert run('r50_upgrade',70)==0
 seed="INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES('11111111-1111-4111-8111-111111111150','r50-retained','r50 retained tenant');"
 sql('r50_upgrade',seed)
 before=sql('r50_upgrade',"SELECT md5(row_to_json(t)::text) FROM platform.tenants t WHERE slug='r50-retained'")
 assert run('r50_upgrade',71)==0 and version('r50_upgrade')==71
 assert sql('r50_upgrade',"SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE oid='investigation.observation_queries'::regclass")=='t'
 assert run('r50_upgrade',72)==0 and version('r50_upgrade')==72
 assert sql('r50_upgrade',"SELECT md5(row_to_json(t)::text) FROM platform.tenants t WHERE slug='r50-retained'")==before
 assert run('r50_upgrade',0)==0
 definition=sql('r50_upgrade',"SELECT pg_get_functiondef('platform.observation_lock_source(uuid,uuid,bigint,bigint,text,text)'::regprocedure)")
 assert "s.source_type='kubernetes'"in definition and "nativeTenant"in definition
 assert sql('r50_upgrade',"SELECT has_function_privilege('worker_runtime_role','platform.observation_lock_source(uuid,uuid,bigint,bigint,text,text)','EXECUTE')")=='t'
 report['cases'].append({'case':'upgrade_70_to71_to72_retry_seed_unchanged_rls_native_guard','passed':True,'version':72,'retainedTenantDigest':before})
 state=json.loads(subprocess.check_output(['docker','inspect',name]))[0];assert state['HostConfig']['NetworkMode']=='none'
 report.update(status='passed',network='none',restrictedMigrationLogin=True,formalOpsctlBootstrap=True,createSchemaGrantedOnlyToOwner=True,negativeTargetRefused=True,nativeArm64SQLRun=False,completedAt=datetime.datetime.now(datetime.timezone.utc).isoformat())
finally:
 if created:call(['docker','rm','-f',name])
 report['testContainerRemoved']=created;(w/'forward-migration-amd64.json').write_text(json.dumps(report,indent=2))
print(json.dumps(report))
