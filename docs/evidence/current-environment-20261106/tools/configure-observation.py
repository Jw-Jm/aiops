from pathlib import Path
import json,hashlib,datetime,subprocess
r=Path('/data/ops-current');ns='ops-current'
def apply(d):subprocess.run(['kubectl','apply','-f','-'],input=json.dumps(d),text=True,check=True,stdout=subprocess.DEVNULL)
cm=json.loads(subprocess.check_output(['kubectl','get','cm','ops-runtime','-n',ns,'-o','json']));sources=[]
for kind,typ,template,datasets,operations in [('metrics','victoriametrics','pod-restarts/v1',['metrics.pod-cpu','metrics.pod-memory','metrics.pod-restarts','metrics.pod-phase'],['trend','aggregate']),('logs','victorialogs','resource-logs/v1',['logs.records'],['samples','aggregate'])]:
 registration=json.loads((r/('evidence/source-'+kind+'.json')).read_text());assert registration['status']==201;s=registration['body']['data'];w=r/('evidence/'+kind);snapshot=json.loads((w/('source-start.json'if kind=='metrics'else'collector-start.json')).read_text());proof=(w/'source-qualification.json').read_bytes();mapping=json.loads(json.dumps(s['dataScopeMapping'],sort_keys=True));binding={'SourceType':typ,'Tenant':snapshot['tenantId'],'SourceID':s['sourceId'],'Revision':s['revision'],'BackendLogicalID':s['backendLogicalId'],'Endpoint':snapshot['endpoint'],'SourceRetentionSeconds':2592000,'ScopeMapping':mapping};scope={'Revision':s['revision'],'Backend':binding['BackendLogicalID'],'Type':typ,'Mapping':mapping};scope_digest='sha256:'+hashlib.sha256(json.dumps(scope,separators=(',',':'),ensure_ascii=False).encode()).hexdigest();qual=[{'continuous':True,'DatasetID':d,'Operation':o,'SourceRevision':s['revision'],'BindingDigest':scope_digest,'EvidenceDigest':'sha256:'+hashlib.sha256(proof).hexdigest(),'CollectorVersion':snapshot['collectorVersion'],'CoverageFrom':snapshot['startedAt'],'MaxWindowSeconds':1800}for d in datasets for o in operations];uid=json.loads(proof)['nativePodUID'];canonical=f'k8s+v1://{snapshot["tenantId"]}/{snapshot["clusterUid"]}/core/Pod/{uid}';sources.append({'Name':typ,'Binding':binding,'CAFile':'/etc/ops/'+kind+'-source/ca.crt','CredentialFile':'/etc/ops/'+kind+'-source/http.json','AnalysisQualifications':qual,'ScopeProbe':{'resourceCanonicalId':canonical,'namespace':ns,'queryTemplate':template,'from':snapshot['startedAt'],'to':datetime.datetime.now(datetime.timezone.utc).isoformat(),'limit':1 if kind=='logs'else 10,'maxBytes':65536,'timeoutMs':3000}})
for duty in ['api','worker']:
 path=r/('config/sp04-'+duty+'.json');c=json.loads(path.read_text());c['Sources']=sources;path.write_text(json.dumps(c,indent=2));cm['data']['sp04-'+duty+'.json']=path.read_text()
apply(cm)
for duty in ['api','worker']:
 d=json.loads(subprocess.check_output(['kubectl','get','deployment','ops-'+duty,'-n',ns,'-o','json']));s=d['spec']['template']['spec']
 if duty=='worker':
  for kind in ['metrics','logs']:
   name=kind+'-source'
   if not any(v['name']==name for v in s['volumes']):s['volumes'].append({'name':name,'secret':{'secretName':'ops-'+kind+'-source','defaultMode':0o440}});s['containers'][0]['volumeMounts'].append({'name':name,'mountPath':'/etc/ops/'+name,'readOnly':True})
 d['spec']['template']['metadata']['annotations']['ops.platform.io/config-version']='observation-1';apply(d)
print('Current verified metrics/logs sources attached to API and Worker')
