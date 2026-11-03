"""Compare persisted sanitized samples with the current native Kubernetes source."""
from pathlib import Path
import datetime,hashlib,json,re,socket,subprocess
assert socket.gethostname()=='mssc'
base=Path('/data/ops-v12-native-visual-r24-20261026');w=base/'r48';ns='ops-native-v12-r24-20261026'
b=json.loads((w/'browser-native-logs.json').read_text());assert b['status']=='passed_native_pod_logs_two_real_queries_and_history'
def kube(*args):return json.loads(subprocess.check_output(['kubectl',*args,'-o','json']))
pod=kube('-n',ns,'get','pod','ops-seaweedfs-0');namespace=kube('get','namespace',ns)
container=pod['spec']['containers'][0]['name'];assert len(pod['spec']['containers'])==1
source=b['source']['source'];assert source['collectorVersion']=='kubernetes-pod-logs/v1' and source['registrationId']=='01a17c3c-ef66-70cf-9ff1-cad25a8bee34'
def instant(value):
 m=re.fullmatch(r'(.*T\d\d:\d\d:\d\d)(?:\.(\d{1,9}))?(Z|[+-]\d\d:\d\d)',value);assert m
 return (int(datetime.datetime.fromisoformat(m[1]+m[3].replace('Z','+00:00')).timestamp()),(m[2] or '').ljust(9,'0'))
def clean(raw):
 raw=re.sub(r'(?i)(bearer\s+)[^\s"\\]+|((?:password|passwd|(?:access|refresh|id|session)[_-]?token|token|secret|authorization|api[_-]?key)\s*[=:]\s*)[^\s,;"\\]+|-----BEGIN [^-]*PRIVATE KEY-----[\s\S]*?-----END [^-]*PRIVATE KEY-----','[REDACTED]',raw)
 raw=re.sub(r'''(?i)(?:password|passwd|(?:access|refresh|id|session)[_-]?token|token|secret|authorization|api[_-]?key)["' ]*[:=]["' ]*[^\s,;"']+''','[REDACTED]',raw)
 return re.sub(r'(?i)(https?://)[^\s/:@]+:[^\s/@]+@',r'\1[REDACTED]@',raw)[:2048]
verified=[]
for entry in b['queries']:
 request,result=entry['request'],entry['result'];assert request['resource']['uid']==pod['metadata']['uid'] and request['resource']['namespaceScope']['namespaceUid']==namespace['metadata']['uid']
 raw=subprocess.check_output(['kubectl','-n',ns,'logs','ops-seaweedfs-0','-c',container,'--timestamps','--since-time='+request['window']['from'],'--limit-bytes=65536'],stderr=subprocess.PIPE).decode()
 available=set()
 for line in raw.splitlines():
  at,sep,message=line.partition(' ')
  if not sep:continue
  try:timekey=instant(at)
  except (AssertionError,ValueError):continue
  if instant(request['window']['from'])<=timekey<instant(request['window']['to']):available.add((timekey,clean(message)))
 for item in result['samples']['items']:assert (instant(item['timestamp']),item['message']) in available,'stored sample not found in actual native log stream'
 assert result['population']=={'basis':'not_measured','method':None} and result['status']=='partial'
 verified.append({'queryId':result['queryId'],'matchedNativeSamples':len(result['samples']['items']),'window':request['window'],'resultDigest':result['resultDigest']})
params={'ids':json.dumps([q['result']['queryId'] for q in b['queries']]),'source':source['registrationId']}
sql="""BEGIN READ ONLY;
select json_build_object('queryId',query_id,'state',state,'source',request->'source','window',request->'window','population',result->'population','sampleCount',result#>>'{samples,returned}','resultDigest',result->>'resultDigest','createdAt',created_at,'expiresAt',expires_at) from investigation.observation_queries where query_id::text in (select jsonb_array_elements_text(:'ids'::jsonb));
select json_build_object('sourceId',source_id,'type',source_type,'revision',revision,'credentialRevision',credential_revision,'status',status) from platform.source_registrations where source_id=:'source';
select json_build_object('oldSnapshotQueryId',query_id,'oldDigest',result->>'resultDigest') from investigation.observation_queries where query_id='e630a865-c1c8-49f4-b3f5-5685ed117df4';
select json_build_object('schema',max(version_id)) from goose_db_version where is_applied;
COMMIT;"""
args=['docker','exec','-i','ops-v12-native-r24-pg','psql','-U','postgres','-d','ui_v12_r24_native','-At','-v','ON_ERROR_STOP=1']
for key,value in params.items():args+=['-v',key+'='+value]
out=subprocess.check_output(args,input=sql,text=True);rows=[json.loads(line) for line in out.splitlines() if line.startswith('{')]
for query in b['queries']:
 row=next(row for row in rows if row.get('queryId')==query['result']['queryId']);assert row['state']=='completed' and row['resultDigest']==query['result']['resultDigest'] and row['source']==query['request']['source'] and row['population']['basis']=='not_measured' and 'count' not in row['population']
assert next(row for row in rows if 'schema' in row)['schema']==72
assert next(row for row in rows if 'oldDigest' in row)['oldDigest']=='sha256:14f0d1e40f283dc68fcaebb0b71d82014d97aa1b6325b77a65af4f3387dd21dd'
registration=next(row for row in rows if 'sourceId' in row);assert registration['type']=='kubernetes' and registration['status']=='active' and registration['revision']==source['registrationRevision'] and registration['credentialRevision']==source['credentialRevision']
configmap=kube('-n',ns,'get','cm','r28-runtime');config=json.loads(configmap['data']['sp04.json']);assert config['Clusters'][0]['PodLogs'] and config['Sources']==[] and not config.get('SP05');profile=json.loads(configmap['data']['sp06.json']);assert profile['assistant']['profile']['taskKinds']==['data_query']
runtime=json.loads((w/'backend-runtime-r48b.json').read_text());components=[]
for artifact in runtime['artifacts']:
 current=kube('-n',ns,'get','pod',artifact['component']);path=current['spec']['containers'][0]['command'][0];assert path==str(w/artifact['artifact']) and hashlib.sha256(Path(path).read_bytes()).hexdigest()==artifact['sha256'] and current['metadata']['uid']==artifact['podUID'];assert current['status']['containerStatuses'][0]['ready'];components.append(artifact)
assert json.loads(subprocess.check_output(['docker','inspect','ops-v12-native-r36b-web-30472']))[0]['State']['Running']
role=kube('-n',ns,'get','role','r48-native-pod-logs');assert role['rules']==[{'apiGroups':[''],'resources':['pods/log'],'verbs':['get']}]
receipt={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'status':'passed_native_log_source_persistence_and_uid_oracle','transaction':'READ ONLY','browserIndexSHA256':b['indexSHA256'],'schema':72,'source':registration,'queries':verified,'rows':rows,'kubernetesIdentity':{'podUID':pod['metadata']['uid'],'namespaceUID':namespace['metadata']['uid'],'container':container,'resourceVersion':pod['metadata']['resourceVersion']},'components':components,'currentRuntimeSnapshotSources':0,'SP05Enabled':False,'modelTaskKinds':['data_query'],'independent30472Running':True,'limits':['Actual on-demand current single container logs, not continuous forwarding/archival ingestion','Source rotation/history completeness and total population remain unknown','No two-cluster, multi-container, full visual, long load or model quality acceptance']}
(w/'native-log-oracle.json').write_text(json.dumps(receipt,indent=2));print('native log samples, persisted digests and current UID/source/runtime independently verified')
