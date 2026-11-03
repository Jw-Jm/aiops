"""Forward-only deployment to the existing owned native task, preserving history."""
from pathlib import Path
import base64,datetime,hashlib,json,os,socket,subprocess,urllib.parse
assert socket.gethostname()=='mssc'
root=Path('/data/ops-v12-native-visual-r24-20261026');work=root/'r37';private=work/'private';ns='ops-native-v12-r24-20261026'
def kube(*args):return subprocess.check_output(['kubectl',*args])
snapshot=json.loads((work/'source-snapshot.json').read_text());registration=json.loads((work/'source-registration.json').read_text())['outcome'];proof=(work/'source-qualification.json').read_bytes()
assert registration['status']==201 and registration['revision']==1
# This database is the task-owned native preview, not the shared development DB.
env=os.environ.copy();env['SP03_MIGRATION_DATABASE_URL']='postgresql://postgres:'+urllib.parse.quote((root/'private/pg-password').read_text().strip(),safe='')+'@127.0.0.1:15454/ui_v12_r24_native?sslmode=disable'
with (work/'migration.log').open('x') as log:subprocess.run([str(work/'ops-v12-r37-db-migrate'),'-dir',str(work/'source/migrations'),'-to','71'],env=env,stdout=log,stderr=subprocess.STDOUT,check=True)
secret={'apiVersion':'v1','kind':'Secret','metadata':{'name':'r37-log-source-http','namespace':ns},'type':'Opaque','data':{'http.json':base64.b64encode((private/'source-http.json').read_bytes()).decode(),'ca.crt':base64.b64encode((private/'tls.crt').read_bytes()).decode()}}
subprocess.run(['kubectl','create','-f','-'],input=json.dumps(secret).encode(),check=True,stdout=subprocess.DEVNULL)
cm=json.loads(kube('get','cm','r28-runtime','-n',ns,'-o','json'));(private/'runtime-before.json').write_text(json.dumps(cm));os.chmod(private/'runtime-before.json',0o600)
config=json.loads(cm['data']['sp04.json']);assert config['Sources']==[]
mapping=registration['dataScopeMapping'];mapping=json.loads(json.dumps(mapping,sort_keys=True))
binding={'SourceType':'victorialogs','Tenant':snapshot['tenantId'],'SourceID':registration['sourceId'],'Revision':1,'BackendLogicalID':'r37-native-pod-log-snapshot','Endpoint':snapshot['endpoint'],'SourceRetentionSeconds':604800,'ScopeMapping':mapping}
scope_doc={'Revision':1,'Backend':binding['BackendLogicalID'],'Type':'victorialogs','Mapping':mapping}
binding_digest='sha256:'+hashlib.sha256(json.dumps(scope_doc,separators=(',',':'),ensure_ascii=False).encode()).hexdigest()
now=datetime.datetime.now(datetime.timezone.utc)
qualifications=[{'resourceUids':[snapshot['podUID']],'DatasetID':'logs.records','Operation':operation,'SourceRevision':1,'BindingDigest':binding_digest,'EvidenceDigest':'sha256:'+hashlib.sha256(proof).hexdigest(),'CollectorVersion':snapshot['collectorVersion'],'VerifiedUntil':(now+datetime.timedelta(hours=24)).isoformat(),'CoverageFrom':snapshot['window']['from'],'CoverageTo':snapshot['window']['to'],'MaxWindowSeconds':1800} for operation in ['samples','aggregate']]
canonical=f'k8s+v1://{snapshot["tenantId"]}/{snapshot["cluster"]["clusterUid"]}/core/Pod/{snapshot["podUID"]}'
config['Sources']=[{'Name':'victorialogs','Binding':binding,'CAFile':'/etc/r37-source/ca.crt','CredentialFile':'/etc/r37-source/http.json','AnalysisQualifications':qualifications,'ScopeProbe':{'resourceCanonicalId':canonical,'namespace':ns,'queryTemplate':'resource-logs/v1','from':snapshot['window']['from'],'to':snapshot['window']['to'],'limit':1,'maxBytes':65536,'timeoutMs':3000}}]
cm['data']['sp04.json']=json.dumps(config,indent=2);(work/'sp04.json').write_text(cm['data']['sp04.json'])
subprocess.run(['kubectl','replace','-f','-'],input=json.dumps(cm).encode(),check=True,stdout=subprocess.DEVNULL)
receipts=[]
for name,artifact in [('ops-worker','ops-v12-r37-worker'),('ops-api','ops-v12-r37-api')]:
 pod=json.loads(kube('get','pod',name,'-n',ns,'-o','json'));(private/(name+'-before.json')).write_text(json.dumps(pod));os.chmod(private/(name+'-before.json'),0o600)
 manifest={'apiVersion':'v1','kind':'Pod','metadata':{'name':name,'namespace':ns,'labels':pod['metadata'].get('labels',{})},'spec':pod['spec']}
 c=manifest['spec']['containers'][0];old=c['command'][0];mount=next(m for m in c['volumeMounts'] if m['mountPath']==old);volume=next(v for v in manifest['spec']['volumes'] if v['name']==mount['name']);volume['hostPath']['path']=str(work/artifact);mount['mountPath']=str(work/artifact);c['command']=[str(work/artifact)]
 if name=='ops-worker':
  manifest['spec']['volumes'].append({'name':'r37-log-source-http','secret':{'secretName':'r37-log-source-http','defaultMode':0o440}})
  c['volumeMounts'].append({'name':'r37-log-source-http','mountPath':'/etc/r37-source','readOnly':True})
 subprocess.run(['kubectl','delete','pod',name,'-n',ns,'--wait=true'],check=True,stdout=subprocess.DEVNULL)
 subprocess.run(['kubectl','create','-f','-'],input=json.dumps(manifest).encode(),check=True,stdout=subprocess.DEVNULL)
 subprocess.run(['kubectl','wait','--for=condition=Ready','pod/'+name,'-n',ns,'--timeout=120s'],check=True,stdout=subprocess.DEVNULL)
 current=json.loads(kube('get','pod',name,'-n',ns,'-o','json'))
 receipts.append({'component':name,'previousPodUID':pod['metadata']['uid'],'podUID':current['metadata']['uid'],'artifact':artifact,'sha256':hashlib.sha256((work/artifact).read_bytes()).hexdigest()})
receipt={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'database':'ui_v12_r24_native','schema':71,'namespace':ns,'artifacts':receipts,'sourceId':registration['sourceId'],'sourceRevision':1,'snapshotOnly':True,'immutableRelease':False}
(work/'backend-runtime.json').write_text(json.dumps(receipt,indent=2));print(json.dumps(receipt))
