import pathlib,subprocess,socket,time,http.client,json,hashlib,datetime,os,re
os.umask(0o077);p=pathlib.Path(__file__).parent/'core-current-r7';e=pathlib.Path('/Users/mssc/Documents/Code/ops/platform/docs/evidence/pre-sp07-20261007');job='01a1145b-78db-7741-b14c-3d244ecade18';ns='ops-pre-sp07-core-r7-20261007';records=[];proc=None;f=None;code=1;receipt={'observedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'jobId':job,'actualSuccessfulModelMCPChainPass':False,'fullRecoveryAcceptance':False,'APIOrWorkerRestartedByThisGate':True,'completeJobTakeoverAndSuccessfulStepNoRerunPass':False,'currentSuccessfulModelMCPChainPass':False,'retainedEarlierDriverContractMismatch':'current-core-r7-failed-investigation-sse-r1.json','wireContract':'internal/httpapi/investigation_handlers.go: typed job.state payload and signed opaque cursor; eventId sequence in Envelope'}
def get(path,cursor=None,tenant=None):
 h=http.client.HTTPConnection('127.0.0.1',port,timeout=15);headers={'Authorization':'Bearer '+(p/'fresh-loa2.token').read_text().strip()}
 if cursor:headers['Last-Event-ID']=cursor
 if tenant:headers['X-Tenant-ID']=tenant
 h.request('GET',path,headers=headers);r=h.getresponse();raw=r.read((1<<20)+1);h.close();assert len(raw)<=1<<20;(p/('api-sigkill-r1-'+str(len(records))+'.private.log')).write_bytes(raw);records.append({'method':'GET','path':path,'status':r.status,'cursorSHA256':'sha256:'+hashlib.sha256(cursor.encode()).hexdigest() if cursor else None,'responseSHA256':'sha256:'+hashlib.sha256(raw).hexdigest()});return r.status,raw
def native(*a):
 args=['kubectl','--context','orbstack',*a,'-o','json'];c=subprocess.run(args,capture_output=True,timeout=30);assert c.returncode==0;(p/('api-sigkill-native-r1-'+str(len(records))+'.private.log')).write_bytes(c.stdout+c.stderr);records.append({'command':args,'exitCode':c.returncode});return json.loads(c.stdout)
def parse_sse(raw):
 out=[]
 for block in raw.decode().split('\n\n'):
  fields={line.split(':',1)[0]:line.split(':',1)[1].strip() for line in block.splitlines() if ':' in line}
  if 'data' in fields:
   b=json.loads(fields['data']);out.append({'seq':int(b['eventId']),'event':fields['event'],'dataSHA256':hashlib.sha256(fields['data'].encode()).hexdigest(),'cursor':fields['id']})
 return out
try:
 with socket.socket() as s:s.bind(('127.0.0.1',0));port=s.getsockname()[1]
 a=['kubectl','--context','orbstack','-n',ns,'port-forward','--address','127.0.0.1','service/ops-api',str(port)+':8080'];f=(p/'api-sigkill-forward-r1.private.log').open('wb');proc=subprocess.Popen(a,stdout=f,stderr=f)
 for _ in range(50):
  assert proc.poll() is None
  with socket.socket() as s:
   s.settimeout(.2)
   if s.connect_ex(('127.0.0.1',port))==0:break
  time.sleep(.2)
 else:raise RuntimeError('own forward unavailable')
 status,raw=get('/api/v1/investigations/'+job);assert status==200;j=json.loads(raw)['data'];assert j['state']=='failed' and j['errorCode']=='INVESTIGATOR_FAILURE' and j['budgetConsumed']['modelRequests']==1 and j['budgetConsumed']['inputTokens']==16384 and j['budgetConsumed']['outputTokens']==1024 and j['budgetConsumed']['toolCalls']==0 and all(v==0 for v in j['budgetReserved'].values());status,raw=get('/api/v1/investigations/'+job+'/events');assert status==200;frames=[]
 for block in raw.decode().split('\n\n'):
  fields={line.split(':',1)[0]:line.split(':',1)[1].strip() for line in block.splitlines() if ':' in line}
  if 'id' in fields:
   body=json.loads(fields['data']);assert body['jobId']==job;frames.append({'id':fields['id'],'event':fields.get('event'),'sequence':int(body['eventId']),'payloadState':body.get('payload',{}).get('state'),'dataSHA256':hashlib.sha256(fields['data'].encode()).hexdigest()})
 assert len(frames)==j['eventSeq'] and len({x['id'] for x in frames})==len(frames);assert frames[-1]['event']=='job.state' and frames[-1]['payloadState']==j['state']=='failed';seq=[x['sequence'] for x in frames]
 assert seq==list(range(1,j['eventSeq']+1));middle=len(frames)//2;status,resumed=get('/api/v1/investigations/'+job+'/events',frames[middle]['id']);assert status==200;resumedFrames=[]
 for block in resumed.decode().split('\n\n'):
  fields={line.split(':',1)[0]:line.split(':',1)[1].strip() for line in block.splitlines() if ':' in line}
  if 'data' in fields:
   body=json.loads(fields['data']);assert body['jobId']==job;resumedFrames.append((int(body['eventId']),hashlib.sha256(fields['data'].encode()).hexdigest()))
 assert resumedFrames==[(x['sequence'],x['dataSHA256']) for x in frames[middle+1:]];foreign='ddca03c9-eba6-456d-b1e2-d28bb8c79525';status,_=get('/api/v1/investigations/'+job+'/events',tenant=foreign);assert status==403;foreignStatus=status
 for eid in ['bb41211a-6f71-56f6-820a-fa1727f0e3ae','bf030ca7-35dd-5779-b115-f6524a211da8']:
  status,raw=get('/api/v1/evidence/'+eid);assert status==200;item=json.loads(raw)['data'];digest='sha256:'+hashlib.sha256(json.dumps(item['factSlice'],separators=(',',':'),ensure_ascii=False).encode()).hexdigest();assert digest==item['contentDigest'] and item['replayState']=='archived_verified' and item['resourceCanonicalId'].endswith('/0d7ecc8c-8e24-4cce-9841-066fbf77b505');receipt.setdefault('retainedSchedulingEvidence',[]).append({'evidenceId':eid,'contentDigest':digest,'archiveVersionId':item['archiveRef']['object']['versionId'],'retainUntil':item['archiveRef']['object']['retainUntil'],'sourceId':item['sourceRegistrationId'],'sourceRevision':item['sourceRevision']})
 # Earlier native failure validation is preserved; restart only the exact owned API container.
 beforeFrames=frames;beforeBudget=j['budgetConsumed'];ready=next(x for x in native('get','pods','-n',ns)['items'] if x['metadata']['name'].startswith('ops-api-'));receipt['apiPodUID']=ready['metadata']['uid'];expected=json.loads((e/'current-core-r7-r9-installed-runtime-identity-r1.json').read_text());row=next(x for x in expected['runtime'] if x['name']==ready['metadata']['name']);assert ready['metadata']['uid']==row['uid'] and ready['metadata']['ownerReferences']==row['ownerReferences'];st=next(x for x in ready['status']['containerStatuses'] if x['name']=='api');oldid=st['containerID'].removeprefix('docker://');a=['docker','--context','orbstack','inspect',oldid];c=subprocess.run(a,capture_output=True,timeout=20);assert c.returncode==0;obj=json.loads(c.stdout)[0];assert obj['Id']==oldid and obj['State']['Running'] and obj['Config']['Labels']['io.kubernetes.pod.uid']==ready['metadata']['uid'] and obj['Config']['Labels']['io.kubernetes.pod.namespace']==ns and obj['Config']['Image'] in [row['containers'][0]['lockedImage'],row['containers'][0]['lockedImage'].split('@',1)[1]] and st['imageID'].removeprefix('docker-pullable://')==row['containers'][0]['lockedImage'];(p/'api-sigkill-before-docker-r1.private.json').write_bytes(c.stdout);plan={'observedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'recordedBeforeSignal':True,'namespace':ns,'podUID':ready['metadata']['uid'],'containerId':oldid,'exactLockedImage':row['containers'][0]['lockedImage'],'nativeDockerImageReference':obj['Config']['Image'],'operation':'SIGKILL exact owned stateless API container; native kubelet restarts; no original data/namespace/storage removal','persistedJobStateBefore':j['state'],'persistedJobEventSeqBefore':j['eventSeq'],'successfulModelChainPass':False};(e/'current-core-r7-api-sigkill-plan-r1.json').write_text(json.dumps(plan,indent=2)+'\n');a=['docker','--context','orbstack','kill','--signal=KILL',oldid];c=subprocess.run(a,capture_output=True,timeout=20);records.append({'command':a,'exitCode':c.returncode});assert c.returncode==0;proc.terminate();proc.wait(timeout=5);f.close();proc=None;f=None
 for _ in range(40):
  current=native('get','pod',ready['metadata']['name'],'-n',ns);assert current['metadata']['uid']==ready['metadata']['uid'];nowst=next(x for x in current['status'].get('containerStatuses',[]) if x['name']=='api')
  if nowst.get('ready') and nowst.get('containerID')!=st['containerID'] and nowst.get('restartCount',0)>st['restartCount']:break
  time.sleep(2)
 else:raise RuntimeError('current native API did not recover from exact SIGKILL')
 receipt.update(apiOldContainerId=oldid,apiNewContainerId=nowst['containerID'],nativeSamePodUID=True,apiRestartCountBefore=st['restartCount'],apiRestartCountAfter=nowst['restartCount'],nativeSIGKILLExitCode=0,nativeAPIRecovered=True);a=['kubectl','--context','orbstack','-n',ns,'port-forward','--address','127.0.0.1','service/ops-api',str(port)+':8080'];f=(p/'api-sigkill-forward-after-r1.private.log').open('wb');proc=subprocess.Popen(a,stdout=f,stderr=f)
 for _ in range(50):
  assert proc.poll() is None
  with socket.socket() as sock:
   sock.settimeout(.2)
   if sock.connect_ex(('127.0.0.1',port))==0:break
  time.sleep(.2)
 else:raise RuntimeError('new own forward unavailable')
 status,raw=get('/api/v1/investigations/'+job);assert status==200;after=json.loads(raw)['data'];assert after['state']==j['state']=='failed' and after['budgetConsumed']==beforeBudget and after['budgetReserved']==j['budgetReserved'] and after['eventSeq']==j['eventSeq'];status,raw=get('/api/v1/investigations/'+job+'/events');assert status==200;actual=parse_sse(raw);assert [(x['seq'],x['event'],x['dataSHA256']) for x in actual]==[(x['sequence'],x['event'],x['dataSHA256']) for x in beforeFrames];status,raw=get('/api/v1/investigations/'+job+'/events',beforeFrames[middle]['id']);assert status==200;actual=parse_sse(raw);assert [(x['seq'],x['dataSHA256']) for x in actual]==[(x['sequence'],x['dataSHA256']) for x in beforeFrames[middle+1:]];status,raw=get('/api/v1/evidence/'+receipt['retainedSchedulingEvidence'][0]['evidenceId']);assert status==200;restored=json.loads(raw)['data'];assert restored['contentDigest']==receipt['retainedSchedulingEvidence'][0]['contentDigest'] and restored['archiveRef']['object']['versionId']==receipt['retainedSchedulingEvidence'][0]['archiveVersionId'];receipt.update(failedStateHonest=True,unknownUsageConservativelyCharged=True,budgetReservedZero=True,sseEventSequence=seq,sseResumeExactSuffix=True,crossTenantStatus=foreignStatus,actualForeignTenantSSE403=True,actualDurableEventsByteDigestUnchangedAfterNativeAPISIGKILL=True,actualPreRestartCursorSuffixAfterRestart=True,archivedEvidenceDigestAndVersionPreserved=True,terminalFailedBudgetAndEventCountUnchangedAfterRestart=True);code=0;print('Exact current R9 API native SIGKILL/restart: original durable Job budgets, event data, pre-restart SSE cursor suffix and retained Evidence digest/version preserved; full takeover/model chain not claimed',flush=True)
except Exception as ex:receipt['failureType']=type(ex).__name__;print('Actual failure/SSE gate failed; raw private evidence retained:',type(ex).__name__,flush=True)
finally:
 if proc:proc.terminate();proc.wait(timeout=5)
 if f:f.close()
 receipt.update(exitCode=code,requests=records,ownedPortForwardStopped=True);(e/'current-core-r7-api-native-sigkill-recovery-r1.json').write_text(json.dumps(receipt,indent=2)+'\n');(e/'current-core-r7-api-native-sigkill-recovery-r1.exit').write_text(str(code)+'\n')
raise SystemExit(code)
