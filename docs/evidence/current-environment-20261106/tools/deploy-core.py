from pathlib import Path
import subprocess,json,base64,os,uuid
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
from cryptography.hazmat.primitives import serialization
r=Path('/data/ops-current');rel=r/'releases/9372674a87201a4ccaab6737a1019808ea41ffd5';p=r/'private';cfg=r/'config';ns='ops-current'
def apply(o):subprocess.run(['kubectl','apply','-f','-'],input=json.dumps(o),text=True,check=True,stdout=subprocess.DEVNULL)
tenant=json.loads((r/'evidence/first-tenant.json').read_text())['tenantId'];urls=json.loads((p/'database-urls.json').read_text());images=json.loads((rel/'images.json').read_text())
if not(p/'context.key').exists():
 k=Ed25519PrivateKey.generate();raw=k.private_bytes(serialization.Encoding.Raw,serialization.PrivateFormat.Raw,serialization.NoEncryption());pub=k.public_key().public_bytes(serialization.Encoding.Raw,serialization.PublicFormat.Raw);(p/'context.key').write_bytes(raw+pub);(p/'context.key').chmod(0o600);(cfg/'context.pub').write_bytes(pub)
common={'IdentityMode':'openbao-kubernetes','Namespace':ns,'CAFile':'/etc/ops/runtime/workload-ca.pem','CRLFile':'/etc/ops/runtime/workload-crl.pem','ServerName':f'ops-worker.{ns}.svc.cluster.local','ContextPublicKeyFile':'/etc/ops/identity/context.pub','ContextPrivateKeyFile':'/etc/ops/identity/context.key','AllowedWorkerCIDRs':['127.0.0.1/32','10.244.0.0/16','192.168.0.63/32'],'ArchiveBackendLogicalID':'current-archive','Clusters':[],'Sources':[],'SP05':{'enabled':True,'analyzer':{'enabled':False},'ingestionBindings':[]}}
for duty in ['api','worker']:
 path=cfg/('sp04-'+duty+'.json')
 if not path.exists():
  v=dict(common,CertificateFile='/etc/ops/identity/'+duty+'.pem',PrivateKeyFile='/etc/ops/identity/'+duty+'.key')
  if duty=='worker':v.update(ListenAddress='127.0.0.1:18282',OwnerEndpoint='https://127.0.0.1:18282')
  path.write_text(json.dumps(v,indent=2))
data={k:(cfg/k).read_text() for k in ['profile.yaml','registry-trust.json','workload-ca.pem','workload-crl.pem','sp04-api.json','sp04-worker.json']};data.update({'bao-ca.pem':(p/'bao-ca.pem').read_text(),'archive-ca.pem':(p/'archive-ca.pem').read_text()})
apply({'apiVersion':'v1','kind':'ConfigMap','metadata':{'name':'ops-runtime','namespace':ns},'data':data})
apply({'apiVersion':'v1','kind':'Secret','metadata':{'name':'ops-context-signing','namespace':ns},'data':{'context.key':base64.b64encode((p/'context.key').read_bytes()).decode(),'context.pub':base64.b64encode((cfg/'context.pub').read_bytes()).decode()}})
apply({'apiVersion':'v1','kind':'Secret','metadata':{'name':'ops-archive-credentials','namespace':ns},'stringData':{'credentials.json':(p/'archive-credentials.json').read_text()}})
base={'HUMAN_AUTH_MODE':'native','NATIVE_AUTH_TENANT_ID':tenant,'PLATFORM_PROFILE':'/etc/ops/runtime/profile.yaml','PLATFORM_REGISTRY_TRUST_FILE':'/etc/ops/runtime/registry-trust.json','OPENBAO_ADDR':f'https://ops-openbao.{ns}.svc.cluster.local:8200','OPENBAO_CA_FILE':'/etc/ops/runtime/bao-ca.pem','OPENBAO_SERVICE_DOMAIN':f'{ns}.svc.cluster.local','OPENBAO_PROJECTED_TOKEN_FILE':'/var/run/secrets/ops-platform/openbao/token','S3_ENDPOINT':f'https://ops-seaweedfs-s3.{ns}.svc.cluster.local:8333','S3_BUCKET':'ops-current-archive','S3_CA_FILE':'/etc/ops/runtime/archive-ca.pem','S3_TENANT_CREDENTIALS_FILE':'/etc/ops/archive/credentials.json'}
for duty in ['api','worker']:
 name='ops-'+duty
 apply({'apiVersion':'v1','kind':'ServiceAccount','metadata':{'name':name,'namespace':ns},'automountServiceAccountToken':False})
 env=dict(base,DATABASE_URL=urls[duty],SP04_RUNTIME_FILE='/etc/ops/runtime/sp04-'+duty+'.json',PLATFORM_METRICS_ADDR='127.0.0.1:'+('19592' if duty=='api' else '19593'))
 if duty=='api':env.update(NATIVE_AUTH_PUBLIC_ORIGIN='https://192.168.0.63:30471',PLATFORM_API_ADDR='127.0.0.1:18592',OPENBAO_MFA_TRANSIT_KEY='ops-current-mfa')
 apply({'apiVersion':'v1','kind':'Secret','metadata':{'name':name+'-env','namespace':ns},'stringData':env})
 volumes=[{'name':'runtime','configMap':{'name':'ops-runtime'}},{'name':'identity','secret':{'secretName':'ops-context-signing','defaultMode':0o440}},{'name':'bao-token','projected':{'defaultMode':0o444,'sources':[{'serviceAccountToken':{'path':'token','audience':'openbao','expirationSeconds':600}}]}},{'name':'tmp','emptyDir':{}}]
 mounts=[{'name':'runtime','mountPath':'/etc/ops/runtime','readOnly':True},{'name':'identity','mountPath':'/etc/ops/identity','readOnly':True},{'name':'bao-token','mountPath':'/var/run/secrets/ops-platform/openbao','readOnly':True},{'name':'tmp','mountPath':'/tmp'}]
 if duty=='worker':
  volumes.append({'name':'archive','secret':{'secretName':'ops-archive-credentials','defaultMode':0o440}});mounts.append({'name':'archive','mountPath':'/etc/ops/archive','readOnly':True})
 container={'name':duty,'image':images[duty]['image'],'imagePullPolicy':'Never','envFrom':[{'secretRef':{'name':name+'-env'}}],'volumeMounts':mounts,'securityContext':{'readOnlyRootFilesystem':True,'allowPrivilegeEscalation':False,'capabilities':{'drop':['ALL']}},'resources':{'requests':{'cpu':'100m','memory':'128Mi'},'limits':({'cpu':'1','memory':'512Mi'} if duty=='worker' else {'cpu':'2','memory':'1Gi'})}}
 if duty=='api':container.update(readinessProbe={'httpGet':{'host':'127.0.0.1','path':'/readyz','port':18592},'periodSeconds':5,'failureThreshold':2},livenessProbe={'httpGet':{'host':'127.0.0.1','path':'/healthz','port':18592},'periodSeconds':10,'failureThreshold':6})
 manifest={'apiVersion':'apps/v1','kind':'Deployment','metadata':{'name':name,'namespace':ns,'annotations':{'ops.platform.io/git-commit':rel.name}},'spec':{'replicas':1,'strategy':{'type':'Recreate'},'selector':{'matchLabels':{'app':name}},'template':{'metadata':{'labels':{'app':name},'annotations':{'ops.platform.io/git-commit':rel.name}},'spec':{'hostNetwork':True,'dnsPolicy':'ClusterFirstWithHostNet','serviceAccountName':name,'automountServiceAccountToken':False,'securityContext':{'runAsNonRoot':True,'runAsUser':65532,'runAsGroup':65532,'fsGroup':65532,'seccompProfile':{'type':'RuntimeDefault'}},'containers':[container],'volumes':volumes}}}}
 (rel/(name+'-deployment.json')).write_text(json.dumps(manifest,indent=2));apply(manifest)
print('Current API/Worker deployments applied with pinned images and independent configuration')
