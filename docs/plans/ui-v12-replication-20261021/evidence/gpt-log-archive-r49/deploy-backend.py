"""Forward-only deployment to the existing owned native task, preserving history."""
from pathlib import Path
import base64,datetime,hashlib,json,os,socket,subprocess,urllib.parse
assert socket.gethostname()=='mssc'
root=Path('/data/ops-v12-native-visual-r24-20261026');work=root/'r49';private=work/'private';ns='ops-native-v12-r24-20261026'
def kube(*args):return subprocess.check_output(['kubectl',*args])
snapshot=json.loads((work/'collector-start.json').read_text());registration=json.loads((work/'source-registration.json').read_text())['outcome'];proof=(work/'source-qualification.json').read_bytes()
assert registration['status']==201 and registration['revision']==1
secret={'apiVersion':'v1','kind':'Secret','metadata':{'name':'r49-log-source-http','namespace':ns},'type':'Opaque','data':{'http.json':base64.b64encode((private/'source-http.json').read_bytes()).decode(),'ca.crt':base64.b64encode((private/'tls.crt').read_bytes()).decode()}}
subprocess.run(['kubectl','create','-f','-'],input=json.dumps(secret).encode(),check=True,stdout=subprocess.DEVNULL)
cm=json.loads(kube('get','cm','r28-runtime','-n',ns,'-o','json'));(private/'runtime-before.json').write_text(json.dumps(cm));os.chmod(private/'runtime-before.json',0o600)
config=json.loads(cm['data']['sp04.json']);assert config['Sources']==[]
mapping=registration['dataScopeMapping'];mapping=json.loads(json.dumps(mapping,sort_keys=True))
binding={'SourceType':'victorialogs','Tenant':snapshot['tenantId'],'SourceID':registration['sourceId'],'Revision':1,'BackendLogicalID':'r49-continuous-pod-logs','Endpoint':snapshot['endpoint'],'SourceRetentionSeconds':2592000,'ScopeMapping':mapping}
scope_doc={'Revision':1,'Backend':binding['BackendLogicalID'],'Type':'victorialogs','Mapping':mapping}
binding_digest='sha256:'+hashlib.sha256(json.dumps(scope_doc,separators=(',',':'),ensure_ascii=False).encode()).hexdigest()
now=datetime.datetime.now(datetime.timezone.utc)
qualifications=[{'continuous':True,'DatasetID':'logs.records','Operation':operation,'SourceRevision':1,'BindingDigest':binding_digest,'EvidenceDigest':'sha256:'+hashlib.sha256(proof).hexdigest(),'CollectorVersion':snapshot['collectorVersion'],'CoverageFrom':snapshot['startedAt'],'MaxWindowSeconds':1800} for operation in ['samples','aggregate']]
uid=json.loads(proof)['nativePodUID'];canonical=f'k8s+v1://{snapshot["tenantId"]}/{snapshot["clusterUid"]}/core/Pod/{uid}'
config['Sources']=[{'Name':'victorialogs','Binding':binding,'CAFile':'/etc/r49-source/ca.crt','CredentialFile':'/etc/r49-source/http.json','AnalysisQualifications':qualifications,'ScopeProbe':{'resourceCanonicalId':canonical,'namespace':ns,'queryTemplate':'resource-logs/v1','from':snapshot['startedAt'],'to':now.isoformat(),'limit':1,'maxBytes':65536,'timeoutMs':3000}}]
cm['data']['sp04.json']=json.dumps(config,indent=2);(work/'sp04.json').write_text(cm['data']['sp04.json'])
subprocess.run(['kubectl','replace','-f','-'],input=json.dumps(cm).encode(),check=True,stdout=subprocess.DEVNULL)
receipts=[]
for name,artifact in [('ops-worker','ops-r49-worker'),('ops-api','ops-r49-api')]:
 pod=json.loads(kube('get','pod',name,'-n',ns,'-o','json'));(private/(name+'-before.json')).write_text(json.dumps(pod));os.chmod(private/(name+'-before.json'),0o600)
 manifest={'apiVersion':'v1','kind':'Pod','metadata':{'name':name,'namespace':ns,'labels':pod['metadata'].get('labels',{})},'spec':pod['spec']}
 c=manifest['spec']['containers'][0];old=c['command'][0];mount=next(m for m in c['volumeMounts'] if m['mountPath']==old);volume=next(v for v in manifest['spec']['volumes'] if v['name']==mount['name']);volume['hostPath']['path']=str(work/artifact);mount['mountPath']=str(work/artifact);c['command']=[str(work/artifact)]
 if name=='ops-worker':
  manifest['spec']['volumes'].append({'name':'r49-log-source-http','secret':{'secretName':'r49-log-source-http','defaultMode':0o440}})
  c['volumeMounts'].append({'name':'r49-log-source-http','mountPath':'/etc/r49-source','readOnly':True})
 subprocess.run(['kubectl','delete','pod',name,'-n',ns,'--wait=true'],check=True,stdout=subprocess.DEVNULL)
 subprocess.run(['kubectl','create','-f','-'],input=json.dumps(manifest).encode(),check=True,stdout=subprocess.DEVNULL)
 subprocess.run(['kubectl','wait','--for=condition=Ready','pod/'+name,'-n',ns,'--timeout=120s'],check=True,stdout=subprocess.DEVNULL)
 current=json.loads(kube('get','pod',name,'-n',ns,'-o','json'))
 receipts.append({'component':name,'previousPodUID':pod['metadata']['uid'],'podUID':current['metadata']['uid'],'artifact':artifact,'sha256':hashlib.sha256((work/artifact).read_bytes()).hexdigest()})
receipt={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'database':'ui_v12_r24_native','schema':72,'namespace':ns,'artifacts':receipts,'sourceId':registration['sourceId'],'sourceRevision':1,'continuousCollector':True,'immutableRelease':False}
(work/'backend-runtime.json').write_text(json.dumps(receipt,indent=2));print(json.dumps(receipt))
