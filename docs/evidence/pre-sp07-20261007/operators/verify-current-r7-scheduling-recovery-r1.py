import pathlib,json,subprocess,datetime,os,time,socket,http.client,hashlib,urllib.parse
os.umask(0o077)
p=pathlib.Path(__file__).parent/'core-current-r7';e=pathlib.Path('/Users/mssc/Documents/Code/ops/platform/docs/evidence/pre-sp07-20261007');ns='ops-pre-sp07-targets-r7-20261007';name='pre-sp07-r7-scheduling-r1';owner='pre-sp07-20261007-r7';old='0d7ecc8c-8e24-4cce-9841-066fbf77b505';records=[];proc=None;f=None;code=1
receipt={'observedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'oldNativePodUID':old,'originalUIDRecovered':False,'SP07Started':False,'sourceScopeChanged':False,'protectedDataDeleted':False,'actualSuccessfulModelMCPChainPass':False}
def run(a,data=None):
 c=subprocess.run(a,input=data,capture_output=True,timeout=60);(p/('scheduling-recovery-r1-'+str(len(records))+'.private.log')).write_bytes(c.stdout+c.stderr);records.append({'command':a,'exitCode':c.returncode});assert c.returncode==0;return c.stdout
def k(*a):return json.loads(run(['kubectl','--context','orbstack',*a,'-o','json']))
def get(path):
 h=http.client.HTTPConnection('127.0.0.1',port,timeout=12);h.request('GET',path,headers={'Authorization':'Bearer '+(p/'fresh-loa2.token').read_text().strip()});r=h.getresponse();raw=r.read((2<<20)+1);h.close();assert len(raw)<=2<<20;(p/('scheduling-recovery-api-r1-'+str(len(records))+'.private.json')).write_bytes(raw);records.append({'method':'GET','path':path,'status':r.status,'responseSHA256':'sha256:'+hashlib.sha256(raw).hexdigest()});return r.status,json.loads(raw)
try:
 nsobj=k('get','namespace',ns);prior=json.loads((e/'current-core-r7-native-scheduling-preparation-r1.json').read_text());assert nsobj['metadata']['uid']==prior['namespaceUID'] and nsobj['metadata']['labels']['ops.platform.io/owner']==owner
 live=k('get','pod',name,'-n',ns);assert live['metadata']['uid']==old and live['metadata']['labels']['ops.platform.io/owner']==owner and live['status']['phase']=='Pending' and not live['spec'].get('nodeName') and not live['spec'].get('volumes') and live['spec']['automountServiceAccountToken']==False and not live['metadata'].get('ownerReferences') and not live['metadata'].get('finalizers');(p/'scheduling-before-recovery-r1.private.json').write_text(json.dumps(live,indent=2))
 plan={'observedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'recordedBeforeDeletion':True,'namespaceUID':nsobj['metadata']['uid'],'podUID':old,'resourceVersion':live['metadata']['resourceVersion'],'ownershipLabel':owner,'noVolumeOrStorageOrController':True,'originalEvidenceVerifiedBeforeDeletion':'current-core-r7-failed-investigation-sse-r4.json','gracefulUIDResourceVersionPreconditions':True,'sharedNodeLabelsUnchanged':True,'historicalIdentityNotRebound':True};(e/'current-core-r7-scheduling-recovery-plan-r1.json').write_text(json.dumps(plan,indent=2)+'\n')
 opts={'apiVersion':'v1','kind':'DeleteOptions','gracePeriodSeconds':30,'propagationPolicy':'Foreground','preconditions':{'uid':old,'resourceVersion':live['metadata']['resourceVersion']}};op=p/'scheduling-delete-options-r1.json';op.write_text(json.dumps(opts));run(['kubectl','--context','orbstack','delete','--raw','/api/v1/namespaces/'+ns+'/pods/'+name,'-f',str(op)])
 for _ in range(40):
  raw=run(['kubectl','--context','orbstack','get','pod',name,'-n',ns,'--ignore-not-found','-o','json'])
  if not raw.strip():break
  time.sleep(.5)
 else:raise RuntimeError('owned graceful deletion did not complete')
 original=json.loads((p/'native-scheduling-target-r1.json').read_text());spec=original['spec'];spec.pop('nodeSelector',None);spec.pop('nodeName',None);spec.pop('tolerations',None);spec['activeDeadlineSeconds']=900;spec['containers'][0]['command']=['/bin/bash','-ec','sleep 850'];obj={'apiVersion':'v1','kind':'Pod','metadata':{'name':name,'namespace':ns,'labels':original['metadata']['labels']},'spec':spec};new=json.loads(run(['kubectl','--context','orbstack','create','-f','-','-o','json'],json.dumps(obj).encode()));assert new['metadata']['uid']!=old;(p/'native-scheduling-restored-target-r1.json').write_text(json.dumps(new,indent=2));receipt['newNativePodUID']=new['metadata']['uid'];run(['kubectl','--context','orbstack','wait','--for=condition=Ready','pod/'+name,'-n',ns,'--timeout=60s']);new=k('get','pod',name,'-n',ns);assert new['status']['phase']=='Running' and new['spec']['nodeName'];node=k('get','node',new['spec']['nodeName']);receipt.update(nativeNewPodReady=True,nodeUID=node['metadata']['uid'],newPodCreationTimestamp=new['metadata']['creationTimestamp'],gracefulUIDResourceVersionPreconditions=True,namespaceUID=nsobj['metadata']['uid'],finiteActiveDeadlineSeconds=900)
 with socket.socket() as s:s.bind(('127.0.0.1',0));port=s.getsockname()[1]
 a=['kubectl','--context','orbstack','-n','ops-pre-sp07-core-r7-20261007','port-forward','--address','127.0.0.1','service/ops-api',str(port)+':8080'];f=(p/'scheduling-recovery-forward-r1.private.log').open('wb');proc=subprocess.Popen(a,stdout=f,stderr=f)
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
 graph=out['data'];current=next(n for n in graph['nodes'] if n['canonicalId']==newcid);assert current['stableId']==new['metadata']['uid'] and current['namespace']==ns and current['name']==name and current['lastObservedAt']>=new['metadata']['creationTimestamp'] and graph['freshness']=='fresh' and not graph['partial'];receipt.update(currentAuthorizedGraphNewUID=True,currentCanonicalId=newcid,currentGraphRevision=graph['graphRevision'],currentGraphFreshness=graph['freshness'],currentLastObservedAt=current['lastObservedAt'],currentNativePhase=new['status']['phase'],oldUIDEvidenceStillReadableAfterRecreation=True,oldEvidenceAutoBoundToNewUID=False,retainedEvidence=evid)
 status,_=get('/api/v1/resources/by-canonical-id?'+urllib.parse.urlencode({'canonicalId':newcid.replace(prior['tenantId'],'ddca03c9-eba6-456d-b1e2-d28bb8c79525',1)}));assert status==403;receipt['crossTenantCanonicalIdStatus']=status;code=0;print('Actual current R9 scheduling recovery: new native UID Ready, fresh authorized Graph, exact old Evidence retained and cross-tenant denied; no successful model claim',flush=True)
except Exception as ex:receipt['failureType']=type(ex).__name__;print('Actual scheduling recovery gate failed; private originals retained:',type(ex).__name__,flush=True)
finally:
 if proc:proc.terminate();proc.wait(timeout=5)
 if f:f.close()
 receipt.update(exitCode=code,commands=records,ownedPortForwardStopped=True);(e/'current-core-r7-scheduling-native-recovery-r1.json').write_text(json.dumps(receipt,indent=2)+'\n');(e/'current-core-r7-scheduling-native-recovery-r1.exit').write_text(str(code)+'\n')
raise SystemExit(code)
