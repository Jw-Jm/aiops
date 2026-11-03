"""One-shot task-owned native cold start. Private inputs never enter reports."""
from pathlib import Path
import json, os, secrets, socket, ssl, subprocess, urllib.request
from urllib.parse import quote
import yaml

root = Path('/data/ops-v12-native-visual-r24-20261026')
private = root / 'private'
ns = 'ops-native-v12-r24-20261026'
db = 'ui_v12_r24_native'
pg = 'ops-v12-native-r24-pg'
def run(args, **kw):
    return subprocess.run(args, check=True, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, **kw).stdout
def save(name, obj):
    p = private / name
    with p.open('x') as f: json.dump(obj, f)
    p.chmod(0o600)
def sql(statement, database='postgres'):
    return run(['docker','exec','-i',pg,'psql','-X','-U','postgres','-d',database,'-v','ON_ERROR_STOP=1'], input=statement)
def apply(doc):
    return run(['kubectl','create','-f','-'],input=json.dumps(doc))

# Root authentication is confined to the newly initialized task instance.
host = f'ops-openbao.{ns}.svc.cluster.local'
ip = run(['kubectl','-n',ns,'get','svc','ops-openbao','-o','jsonpath={.spec.clusterIP}']).strip()
lookup = socket.getaddrinfo
socket.getaddrinfo = lambda name,*a,**kw: lookup(ip if name == host else name,*a,**kw)
token = json.loads((private/'bao-recovery.json').read_text())['rootToken']
request = urllib.request.Request(f'https://{host}:8200/v1/transit/keys/ops-native-mfa',data=json.dumps({'type':'aes256-gcm96','exportable':False,'allow_plaintext_backup':False}).encode(),headers={'X-Vault-Token':token,'Content-Type':'application/json'},method='POST')
with urllib.request.urlopen(request,context=ssl.create_default_context(cafile=str(private/'bao-ca.pem'))) as response: response.read()
args = [str(root/'bin/ops-r24-opsctl'),'openbao','configure','--profile',str(root/'resolved-profile.yaml'),'--ca-file',str(private/'bao-ca.pem'),'--recovery-file',str(private/'bao-recovery.json'),'--investigation-signing','--native-mfa-key','ops-native-mfa']
(root/'evidence/native-bao-configure.json').write_text(run(args,cwd=root/'source'))

# An empty dedicated database; migrations and first tenant run as distinct duties.
sql(f'CREATE DATABASE {db} OWNER postgres;')
owner = ((private/'pg-password').read_text().strip())
env = dict(os.environ, SP03_MIGRATION_DATABASE_URL=f'postgres://postgres:{quote(owner,safe="")}@127.0.0.1:15454/{db}?sslmode=disable')
(root/'evidence/migration-bootstrap.log').write_text(run(['/data/ops-v12-completion-20261026-r1/db-migrate','-dir','/data/ops-v12-completion-20261026-r1/source/migrations','-to','1'],env=env))
sql(f'GRANT CREATE ON DATABASE {db} TO schema_owner;')
(root/'evidence/migration.log').write_text(run(['/data/ops-v12-completion-20261026-r1/db-migrate','-dir','/data/ops-v12-completion-20261026-r1/source/migrations'],env=env))
urls = {}
for duty, role in [('api','api_runtime_role'),('worker','worker_runtime_role'),('bootstrap','migration_role'),('cli','ops_readonly_role')]:
    name = f'ui_v12_r24_{duty}'
    password = secrets.token_hex(32)
    sql(f"CREATE ROLE {name} LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '{password}'; GRANT {role} TO {name}; GRANT CONNECT ON DATABASE {db} TO {name};")
    urls[duty] = f'postgres://{name}:{password}@127.0.0.1:15454/{db}?sslmode=disable'
save('database-urls.json',urls)
password = secrets.token_urlsafe(30)
(private/'initial-password').write_text(password); (private/'initial-password').chmod(0o600)
save('bootstrap.json',{'databaseUrl':urls['bootstrap'],'tenantSlug':'v12-r24-native','tenantDisplayName':'V1.2 独立原生验收','adminLoginName':'v12.r24.admin','adminDisplayName':'原生验收管理员'})
receipt = json.loads(run([str(root/'bin/ops-r24-opsctl'),'bootstrap','first-tenant-native','--input',str(private/'bootstrap.json'),'--password-file',str(private/'initial-password')],cwd=root/'source'))
(root/'evidence/first-tenant.json').write_text(json.dumps(receipt,indent=2))
save('browser-account.json',{'loginName':receipt['loginName'],'password':password,'userId':receipt['userId']})
tenant = receipt['tenantId']
credential = lambda: {'accessKey':secrets.token_hex(12),'secretKey':secrets.token_hex(32)}
admin = credential()
credentials = {'schemaVersion':'ops-archive-credentials/v2','tenants':[{'tenantId':tenant,'roles':{role:credential() for role in ['write','read','protect','cleanup']}}]}
save('archive-credentials.json',credentials)
save('archive-iam-input.json',{'schemaVersion':1,'bucket':'ops-v12-r24-archive','bootstrapAdmin':admin,'credentials':credentials})
(root/'evidence/archive-iam.json').write_text(run([str(root/'bin/ops-r24-opsctl'),'bootstrap','archive-iam','--secrets-file',str(private/'archive-iam-input.json'),'--output',str(private/'archive-iam.json')],cwd=root/'source'))
archivehost = f'ops-seaweedfs-s3.{ns}.svc.cluster.local'
run(['openssl','req','-x509','-newkey','rsa:3072','-nodes','-days','30','-subj','/CN=ops-v12-r24-archive-ca','-keyout',str(private/'archive-ca.key'),'-out',str(private/'archive-ca.pem'),'-addext','basicConstraints=critical,CA:TRUE'])
run(['openssl','req','-new','-newkey','rsa:3072','-nodes','-subj',f'/CN={archivehost}','-keyout',str(private/'archive-tls.key'),'-out',str(private/'archive.csr')])
(private/'archive.ext').write_text(f'subjectAltName=DNS:{archivehost}\nextendedKeyUsage=serverAuth\nbasicConstraints=critical,CA:FALSE\n')
run(['openssl','x509','-req','-in',str(private/'archive.csr'),'-CA',str(private/'archive-ca.pem'),'-CAkey',str(private/'archive-ca.key'),'-CAcreateserial','-days','30','-extfile',str(private/'archive.ext'),'-out',str(private/'archive-tls.pem')])
for f in private.iterdir(): f.chmod(0o600)
for name, data in [('r24-archive-iam',{'s3.json':(private/'archive-iam.json').read_text()}),('r24-archive-root',{'username':admin['accessKey'],'password':admin['secretKey']}),('r24-archive-tls',{'tls.crt':(private/'archive-tls.pem').read_text(),'tls.key':(private/'archive-tls.key').read_text()})]:
    apply({'apiVersion':'v1','kind':'Secret','metadata':{'name':name,'namespace':ns},'type':'Opaque','stringData':data})
profile = yaml.safe_load((root/'resolved-profile.yaml').read_text())
profile['components']['seaweedfs']['endpoint'] = f'https://{archivehost}:8333'
(root/'resolved-profile.yaml').write_text(yaml.safe_dump(profile,allow_unicode=True))
values = {'components':{'seaweedfs':{'mode':'bundled','endpoint':profile['components']['seaweedfs']['endpoint'],'image':'docker.io/chrislusf/seaweedfs@sha256:ce9e796f1fe6f06968f4c04bdaf8f678dad9c8acdfef3d244133d71bfa6bf882','iamSecretName':'r24-archive-iam','tlsSecretName':'r24-archive-tls','auth':{'existingSecret':'r24-archive-root','usernameKey':'username','passwordKey':'password'}}}}
(root/'archive-values.json').write_text(json.dumps(values))
docs = list(yaml.safe_load_all(run(['helm','template','ops-r24',str(root/'source/deploy/charts/ops-dependencies'),'-n',ns,'-f',str(root/'archive-values.json'),'-s','templates/seaweedfs.yaml'])))
data = root/'archive-data'; data.mkdir(); os.chown(data,10001,10001); data.chmod(0o770)
for doc in docs:
    doc['metadata']['namespace'] = ns
    if doc['kind']=='StatefulSet':
        doc['spec'].pop('volumeClaimTemplates')
        doc['spec']['template']['spec']['volumes'].append({'name':'data','hostPath':{'path':str(data),'type':'Directory'}})
    apply(doc)

apply({'apiVersion':'v1','kind':'ConfigMap','metadata':{'name':'r24-public-runtime','namespace':ns},'data':{'profile.yaml':(root/'resolved-profile.yaml').read_text(),'bao-ca.pem':(private/'bao-ca.pem').read_text(),'archive-ca.pem':(private/'archive-ca.pem').read_text()}})
apply({'apiVersion':'v1','kind':'Secret','metadata':{'name':'r24-worker-archive','namespace':ns},'stringData':{'credentials.json':json.dumps(credentials)}})
base = {'HUMAN_AUTH_MODE':'native','NATIVE_AUTH_TENANT_ID':tenant,'PLATFORM_PROFILE':'/etc/ops/runtime/profile.yaml','OPENBAO_ADDR':f'https://{host}:8200','OPENBAO_CA_FILE':'/etc/ops/runtime/bao-ca.pem','OPENBAO_SERVICE_DOMAIN':f'{ns}.svc.cluster.local','OPENBAO_PROJECTED_TOKEN_FILE':'/var/run/secrets/ops-platform/openbao/token','S3_ENDPOINT':profile['components']['seaweedfs']['endpoint'],'S3_BUCKET':'ops-v12-r24-archive','S3_CA_FILE':'/etc/ops/runtime/archive-ca.pem','S3_TENANT_CREDENTIALS_FILE':'/etc/ops/archive/credentials.json'}
prior = json.loads(Path('/data/ops-v12-completion-20261026-r1/private/api-env.json').read_text())
base['PLATFORM_REGISTRY_TRUST_FILE'] = prior.get('PLATFORM_REGISTRY_TRUST_FILE', str(root/'registry-trust.json'))
for duty in ['api','worker']:
    config = dict(base,DATABASE_URL=urls[duty],PLATFORM_METRICS_ADDR='127.0.0.1:'+('19092' if duty=='api' else '19093'))
    if duty=='api': config.update(NATIVE_AUTH_PUBLIC_ORIGIN='https://192.168.0.63:30471',PLATFORM_API_ADDR='127.0.0.1:18092',OPENBAO_MFA_TRANSIT_KEY='ops-native-mfa')
    # Registry verification material is public. Keep original path read-only.
    trust = Path(base['PLATFORM_REGISTRY_TRUST_FILE'])
    apply({'apiVersion':'v1','kind':'ServiceAccount','metadata':{'name':'ops-'+duty,'namespace':ns},'automountServiceAccountToken':False})
    pod = {'apiVersion':'v1','kind':'Pod','metadata':{'name':'ops-'+duty,'namespace':ns},'spec':{'hostNetwork':True,'dnsPolicy':'ClusterFirstWithHostNet','serviceAccountName':'ops-'+duty,'automountServiceAccountToken':False,'restartPolicy':'Always','containers':[{'name':duty,'image':'ops.local/verification-20261019/platform-'+duty+('@sha256:494817ba463482a5959c1c3e10968b3e28db1affc458dbdbe2cec0be5d4878a7' if duty=='api' else '@sha256:48d3700cd7595781e5384821b07e8fa38aaf172ada0fe2b55832fedd661a92ad'),'imagePullPolicy':'Never','command':[str(root/('bin/ops-r24-'+duty))],'env':[{'name':k,'value':v} for k,v in config.items()],'volumeMounts':[{'name':'binary','mountPath':str(root/('bin/ops-r24-'+duty)),'readOnly':True},{'name':'runtime','mountPath':'/etc/ops/runtime','readOnly':True},{'name':'trust','mountPath':str(trust),'readOnly':True},{'name':'projected','mountPath':'/var/run/secrets/ops-platform/openbao','readOnly':True}]}],'volumes':[{'name':'binary','hostPath':{'path':str(root/('bin/ops-r24-'+duty)),'type':'File'}},{'name':'runtime','configMap':{'name':'r24-public-runtime'}},{'name':'trust','hostPath':{'path':str(trust),'type':'File'}},{'name':'projected','projected':{'defaultMode':0o444,'sources':[{'serviceAccountToken':{'path':'token','audience':'openbao','expirationSeconds':600}}]}}]}}
    if duty=='worker':
        pod['spec']['containers'][0]['volumeMounts'].append({'name':'archive-credentials','mountPath':'/etc/ops/archive','readOnly':True})
        pod['spec']['volumes'].append({'name':'archive-credentials','secret':{'secretName':'r24-worker-archive','defaultMode':0o444}})
    assert all(v.get('hostPath', {}).get('path') != str(root) for v in pod['spec']['volumes'])
    assert all('OIDC' not in k for k in config)
    # The private environment is installed as a namespace Secret, not a manifest.
    apply({'apiVersion':'v1','kind':'Secret','metadata':{'name':'ops-'+duty+'-env','namespace':ns},'stringData':config})
    pod['spec']['containers'][0].pop('env')
    pod['spec']['containers'][0]['envFrom']=[{'secretRef':{'name':'ops-'+duty+'-env'}}]
    (root/f'{duty}-pod.json').write_text(json.dumps(pod,indent=2))
    apply(pod)
print(json.dumps({'database':db,'tenantId':tenant,'namespace':ns,'schema':70,'oidcEnvironment':False,'projectedTTL':600,'origin':'https://192.168.0.63:30471'}))
