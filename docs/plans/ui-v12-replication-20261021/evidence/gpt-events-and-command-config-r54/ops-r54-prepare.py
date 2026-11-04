from pathlib import Path
import base64,datetime,hashlib,json,socket,subprocess,uuid
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
assert socket.gethostname()=='mssc'
root=Path('/data/ops-v12-native-visual-r24-20261026');w=root/'r54';w.mkdir(exist_ok=True);private=w/'private';private.mkdir(mode=0o700,exist_ok=True);ns='ops-native-v12-r24-20261026';tenant='01a177f1-372f-75b7-be90-e8121d1445f5';cluster='79014400-9453-4cb4-9678-0fae47981d79'
def kube(*a):return subprocess.check_output(['kubectl',*a],stderr=subprocess.PIPE)
def create(obj):return json.loads(subprocess.check_output(['kubectl','create','-f','-','-o','json'],input=json.dumps(obj).encode(),stderr=subprocess.PIPE))
pvc=create({'apiVersion':'v1','kind':'PersistentVolumeClaim','metadata':{'name':'ops-r54-unbound','namespace':ns,'labels':{'ops-check':'r54'}},'spec':{'accessModes':['ReadWriteOnce'],'storageClassName':'ops-r54-manual','resources':{'requests':{'storage':'1Mi'}}}})
uid=pvc['metadata']['uid'];canonical='k8s+v1://'+tenant+'/'+cluster+'/core/PersistentVolumeClaim/'+uid
sa='ops-r54-profile-read'
objects=[{'apiVersion':'v1','kind':'ServiceAccount','metadata':{'name':sa,'namespace':ns},'automountServiceAccountToken':False},{'apiVersion':'rbac.authorization.k8s.io/v1','kind':'Role','metadata':{'name':sa,'namespace':ns},'rules':[{'apiGroups':[''],'resources':['persistentvolumeclaims'],'resourceNames':['ops-r54-unbound'],'verbs':['get']}]},{'apiVersion':'rbac.authorization.k8s.io/v1','kind':'RoleBinding','metadata':{'name':sa,'namespace':ns},'roleRef':{'apiGroup':'rbac.authorization.k8s.io','kind':'Role','name':sa},'subjects':[{'kind':'ServiceAccount','name':sa,'namespace':ns}]},{'apiVersion':'networking.k8s.io/v1','kind':'NetworkPolicy','metadata':{'name':sa,'namespace':ns},'spec':{'podSelector':{'matchLabels':{'app':sa}},'policyTypes':['Ingress','Egress'],'ingress':[],'egress':[{'to':[{'ipBlock':{'cidr':'192.168.0.63/32'}}],'ports':[{'protocol':'TCP','port':6443}]}]}}]
for obj in objects:create(obj)
image='ops.local/verification-20261019/command-runner@sha256:90c61be5eca27f8c8a9aed37a91bb4f74f9f5c6b7b7af2a24eea1772bce99d9e'
assert image in subprocess.check_output(['sudo','ctr','-n','k8s.io','images','ls','-q']).decode().splitlines()
profile={'schemaVersion':'execution-profile/v2','executionProfileId':str(uuid.uuid4()),'version':1,'name':'r54-read-pvc','type':'k8s_namespace','allowedTargets':[canonical],'clusterUid':cluster,'namespace':ns,'toolImageDigest':image,'tools':['kubectl'],'networkPolicyRef':sa,'credentialRef':'serviceaccount:/'+ns+'/'+sa,'timeoutSeconds':30,'maxOutputBytes':65536,'principal':'','hostOnboardingRef':'','developmentFallback':False}
canonicalJSON=lambda v:json.dumps(v,sort_keys=True,separators=(',',':'),ensure_ascii=False).encode()
key=(root/'r28-worker/private/policy-key').read_bytes();signer=Ed25519PrivateKey.from_private_bytes(key[:32]);message=canonicalJSON({'domain':'ops-execution-profile/v2','tenantId':tenant,'profile':profile})
body={'profile':profile,'signerKeyId':'r28-task-policy','signature':base64.b64encode(signer.sign(message)).decode()};(w/'profile-publication.json').write_text(json.dumps(body,indent=2))
cm=json.loads(kube('get','cm','r28-runtime','-n',ns,'-o','json'));(private/'runtime-before.json').write_text(json.dumps(cm));runtime=json.loads(cm['data']['sp04.json']);runtime['SP05']={'Enabled':True,'Analyzer':{'Enabled':False},'IngestionBindings':[]};cm['data']['sp04.json']=json.dumps(runtime);cm['data']['sp07.json']=json.dumps({'schemaVersion':'sp07-runtime/v1','configurationOnly':True,'namespace':ns,'tenants':[tenant],'admittedProfiles':[profile]})
for name in ['ops-api','ops-worker']:
 p=json.loads(kube('get','pod',name,'-n',ns,'-o','json'));(private/(name+'-before.json')).write_text(json.dumps(p));m={'apiVersion':'v1','kind':'Pod','metadata':{'name':name,'namespace':ns,'labels':p['metadata'].get('labels',{})},'spec':p['spec']};c=m['spec']['containers'][0];artifact=w/('ops-r54-api'if name=='ops-api'else'ops-r54-worker');assert artifact.is_file();old=c['command'][0];mount=next(v for v in c['volumeMounts']if v['mountPath']==old);volume=next(v for v in m['spec']['volumes']if v['name']==mount['name']);mount['mountPath']=str(artifact);volume['hostPath']['path']=str(artifact);c['command']=[str(artifact)];c['env']=[v for v in c['env']if v['name']!='SP07_RUNTIME_FILE']+[{'name':'SP07_RUNTIME_FILE','value':'/etc/r28/sp07.json'}];(private/(name+'-next.json')).write_text(json.dumps(m))
# Change only this task's ConfigMap and two existing Pods; backups preserve private values.
subprocess.run(['kubectl','replace','-f','-'],input=json.dumps(cm).encode(),check=True,stdout=subprocess.DEVNULL)
receipts=[]
for name in ['ops-worker','ops-api']:
 kube('delete','pod',name,'-n',ns,'--wait=true','--timeout=35s');subprocess.run(['kubectl','create','-f',str(private/(name+'-next.json'))],check=True,stdout=subprocess.DEVNULL)
 receipts.append({'component':name,'sha256':hashlib.sha256((w/('ops-r54-api'if name=='ops-api'else'ops-r54-worker')).read_bytes()).hexdigest()})
report={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'tenantId':tenant,'clusterUid':cluster,'namespace':ns,'probeUID':uid,'probeCanonicalId':canonical,'probeName':'ops-r54-unbound','profileId':profile['executionProfileId'],'sp05Enabled':True,'executionConfigurationOnly':True,'runnerStarted':False,'artifacts':receipts};(w/'deployment.json').write_text(json.dumps(report,indent=2));print(json.dumps(report))
