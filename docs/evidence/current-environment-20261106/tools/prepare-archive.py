from pathlib import Path
import secrets,json,subprocess,os,yaml,uuid
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
from cryptography.hazmat.primitives import serialization
r=Path('/data/ops-current');p=r/'private';c=r/'config';release=r/'releases/9372674a87201a4ccaab6737a1019808ea41ffd5';ns='ops-current'
def run(a,**kw):return subprocess.check_output(a,stderr=subprocess.PIPE,**kw)
def save(name,j):
 path=p/name
 if not path.exists():path.write_text(json.dumps(j));path.chmod(0o600)
 return json.loads(path.read_text())
tenant=json.loads((r/'evidence/first-tenant.json').read_text())['tenantId'];cred=lambda:{'accessKey':secrets.token_hex(12),'secretKey':secrets.token_hex(32)}
admin=save('archive-admin.json',cred());credentials=save('archive-credentials.json',{'schemaVersion':'ops-archive-credentials/v2','tenants':[{'tenantId':tenant,'roles':{role:cred() for role in ['write','read','protect','cleanup']}}]})
save('archive-iam-input.json',{'schemaVersion':1,'bucket':'ops-current-archive','bootstrapAdmin':admin,'credentials':credentials})
if not(p/'archive-iam.json').exists():run([str(release/'bin/opsctl'),'bootstrap','archive-iam','--secrets-file',str(p/'archive-iam-input.json'),'--output',str(p/'archive-iam.json')],cwd=release)
if not(p/'archive-ca.pem').exists():
 run(['openssl','req','-x509','-newkey','rsa:3072','-nodes','-days','365','-subj','/CN=ops-current-archive-ca','-keyout',str(p/'archive-ca.key'),'-out',str(p/'archive-ca.pem'),'-addext','basicConstraints=critical,CA:TRUE','-addext','keyUsage=critical,keyCertSign,cRLSign'])
 host='ops-seaweedfs-s3.ops-current.svc.cluster.local'
 run(['openssl','req','-new','-newkey','rsa:3072','-nodes','-subj','/CN='+host,'-keyout',str(p/'archive-tls.key'),'-out',str(p/'archive.csr')])
 (p/'archive.ext').write_text(f'subjectAltName=DNS:{host}\nextendedKeyUsage=serverAuth\nbasicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\n')
 run(['openssl','x509','-req','-in',str(p/'archive.csr'),'-CA',str(p/'archive-ca.pem'),'-CAkey',str(p/'archive-ca.key'),'-CAcreateserial','-days','365','-extfile',str(p/'archive.ext'),'-out',str(p/'archive-tls.pem')])
if not(p/'registry-key').exists():
 import base64
 k=Ed25519PrivateKey.generate();raw=k.private_bytes(serialization.Encoding.Raw,serialization.PrivateFormat.Raw,serialization.NoEncryption());pub=k.public_key().public_bytes(serialization.Encoding.Raw,serialization.PublicFormat.Raw)
 (p/'registry-key').write_bytes(raw+pub);(c/'registry-trust.json').write_text(json.dumps({'current-registry':base64.b64encode(pub).decode()}))
bao=json.loads(run(['kubectl','get','secret','ops-openbao-bootstrap-tls','-n',ns,'-o','json']))['data']
import base64
secret=[{'name':'ops-postgresql-auth','data':{'username':'postgres','password':(p/'pg-password').read_text()}},{'name':'ops-openbao-bootstrap-tls','data':{k:base64.b64decode(v).decode() for k,v in bao.items()}},{'name':'ops-seaweedfs-auth','data':admin},{'name':'ops-seaweedfs-tls','data':{'tls.crt':(p/'archive-tls.pem').read_text(),'tls.key':(p/'archive-tls.key').read_text()}},{'name':'ops-seaweedfs-iam','data':{'s3.json':(p/'archive-iam.json').read_text()}}]
save('environment-secrets.json',secret)
(c/'environment.json').write_text(json.dumps({'schemaVersion':1,'context':'kubernetes-admin@kubernetes','namespace':ns,'humanAuthMode':'native','publicTrust':{'openbao-ca.pem':(p/'bao-ca.pem').read_text(),'archive-ca.pem':(p/'archive-ca.pem').read_text(),'archive-bucket':'ops-current-archive','registry-trust.json':(c/'registry-trust.json').read_text()}}))
for f in p.iterdir():
 if f.is_file():f.chmod(0o600)
print('Fresh archive IAM, TLS, registry trust and formal environment bootstrap inputs prepared privately')
