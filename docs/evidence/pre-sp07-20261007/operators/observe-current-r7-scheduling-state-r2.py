import pathlib,json,subprocess,datetime,os,time,socket,http.client,hashlib,urllib.parse
os.umask(0o077)
p=pathlib.Path(__file__).parent/'core-current-r7';e=pathlib.Path('/Users/mssc/Documents/Code/ops/platform/docs/evidence/pre-sp07-20261007');ns='ops-pre-sp07-targets-r7-20261007';name='pre-sp07-r7-scheduling-r1';owner='pre-sp07-20261007-r7';old='0d7ecc8c-8e24-4cce-9841-066fbf77b505';records=[];proc=None;f=None;code=1
receipt={'observedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'oldNativePodUID':old,'originalUIDRecovered':False,'SP07Started':False,'sourceScopeChanged':False,'protectedDataDeleted':False,'actualSuccessfulModelMCPChainPass':False}
def run(a,data=None):
 c=subprocess.run(a,input=data,capture_output=True,timeout=60);(p/('scheduling-state-observation-r2-'+str(len(records))+'.private.log')).write_bytes(c.stdout+c.stderr);records.append({'command':a,'exitCode':c.returncode});assert c.returncode==0;return c.stdout
def k(*a):return json.loads(run(['kubectl','--context','orbstack',*a,'-o','json']))
def get(path):
 h=http.client.HTTPConnection('127.0.0.1',port,timeout=12);h.request('GET',path,headers={'Authorization':'Bearer '+(p/'fresh-loa2.token').read_text().strip()});r=h.getresponse();raw=r.read((2<<20)+1);h.close();assert len(raw)<=2<<20;(p/('scheduling-state-api-observation-r2-'+str(len(records))+'.private.json')).write_bytes(raw);records.append({'method':'GET','path':path,'status':r.status,'responseSHA256':'sha256:'+hashlib.sha256(raw).hexdigest()});return r.status,json.loads(raw)
try:
 prior=json.loads((e/'current-core-r7-native-scheduling-preparation-r1.json').read_text());previous=json.loads((e/'current-core-r7-scheduling-native-recovery-r1.json').read_text());assert previous['exitCode']==1 and previous['nativeNewPodReady'] and previous['gracefulUIDResourceVersionPreconditions'];receipt['retainedFullGraphGateFailure']='current-core-r7-scheduling-native-recovery-r1.json';receipt['completeNondegradedGraphPass']=False
 nsobj=k('get','namespace',ns);assert nsobj['metadata']['uid']==prior['namespaceUID'] and nsobj['metadata']['labels']['ops.platform.io/owner']==owner;new=k('get','pod',name,'-n',ns);assert new['metadata']['uid']==previous['newNativePodUID'] and new['status']['phase']=='Running' and any(c['type']=='Ready' and c['status']=='True' for c in new['status']['conditions']);receipt.update(newNativePodUID=new['metadata']['uid'],nativeNewPodReady=True,newPodCreationTimestamp=new['metadata']['creationTimestamp'],namespaceUID=nsobj['metadata']['uid'],finiteActiveDeadlineSeconds=900,nativePhaseRecoveryObservationOnly=True)
 with socket.socket() as s:s.bind(('127.0.0.1',0));port=s.getsockname()[1]
 a=['kubectl','--context','orbstack','-n','ops-pre-sp07-core-r7-20261007','port-forward','--address','127.0.0.1','service/ops-api',str(port)+':8080'];f=(p/'scheduling-state-forward-r2.private.log').open('wb');proc=subprocess.Popen(a,stdout=f,stderr=f)
 for _ in range(50):
  assert proc.poll() is None
  with socket.socket() as s:
   s.settimeout(.2)
   if s.connect_ex(('127.0.0.1',port))==0:break
  time.sleep(.2)
 else:raise RuntimeError('own forward unavailable')
 evid=[];oldcid=None
 for eid in ['bb41211a-6f71-56f6-820a-fa1727f0e3ae','bf030ca7-35dd-5779-b115-f6524a211da8']:
  status,item=get('/api/v1/evidence/'+eid);assert status==200;item=item['data'];digest='sha256:'+hashlib.sha256(json.dumps(item['factSlice'],separators=(',',':'),ensure_ascii=False).encode()).hexdigest();assert digest==item['contentDigest'] and item['resourceCanonicalId'].endswith('/'+old) and item['replayState']=='archived_verified';oldcid=item['resourceCanonicalId'];evid.append({'evidenceId':eid,'contentDigest':digest,'canonicalId':oldcid,'versionId':item['archiveRef']['object']['versionId'],'retainUntil':item['archiveRef']['object']['retainUntil']})
 newcid=oldcid.rsplit('/',1)[0]+'/'+new['metadata']['uid'];path='/api/v1/resources/by-canonical-id?'+urllib.parse.urlencode({'canonicalId':newcid})
 for _ in range(30):
  status,out=get(path)
  if status==200 and any(n['canonicalId']==newcid for n in out['data']['nodes']):break
  time.sleep(2)
 else:raise RuntimeError('new UID not visible in current authorized graph')
 graph=out['data'];current=next(n for n in graph['nodes'] if n['canonicalId']==newcid);assert current['stableId']==new['metadata']['uid'] and current['namespace']==ns and current['name']==name and current['lastObservedAt']>=new['metadata']['creationTimestamp'] and graph['freshness']=='fresh';assert graph['partial'] and sorted(graph['degradedSources'])==sorted([x['sourceId']+'/proof' for x in json.loads((e/'current-core-r7-formal-api-initialization-r5.json').read_text())['sources'] if x['sourceType'] in ['victoriametrics','victorialogs']]) and graph['warnings']==['source_scope_unverified','source_scope_unverified'];receipt.update(currentGraphPartial=True,currentDegradedSources=graph['degradedSources'],currentWarnings=graph['warnings'],VictoriaScopeProofAcceptance=False);receipt.update(currentAuthorizedGraphNewUID=True,currentCanonicalId=newcid,currentGraphRevision=graph['graphRevision'],currentGraphFreshness=graph['freshness'],currentLastObservedAt=current['lastObservedAt'],currentNativePhase=new['status']['phase'],oldUIDEvidenceStillReadableAfterRecreation=True,oldEvidenceAutoBoundToNewUID=False,retainedEvidence=evid)
 status,_=get('/api/v1/resources/by-canonical-id?'+urllib.parse.urlencode({'canonicalId':newcid.replace(prior['tenantId'],'ddca03c9-eba6-456d-b1e2-d28bb8c79525',1)}));assert status==403;receipt['crossTenantCanonicalIdStatus']=status;code=0;print('Current native scheduling phase recovery and exact historical Evidence observed; whole Graph remains partial from two unverified Victoria sources; original full Graph gate stays failed',flush=True)
except Exception as ex:receipt['failureType']=type(ex).__name__;print('Actual scheduling recovery gate failed; private originals retained:',type(ex).__name__,flush=True)
finally:
 if proc:proc.terminate();proc.wait(timeout=5)
 if f:f.close()
 receipt.update(exitCode=code,commands=records,ownedPortForwardStopped=True);(e/'current-core-r7-scheduling-state-observation-r2.json').write_text(json.dumps(receipt,indent=2)+'\n');(e/'current-core-r7-scheduling-state-observation-r2.exit').write_text(str(code)+'\n')
raise SystemExit(code)
