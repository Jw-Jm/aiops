import pathlib,json,subprocess,datetime,hashlib,urllib.request,urllib.error,time,socket,collections,concurrent.futures,os
r=pathlib.Path('/Users/mssc/Documents/Code/ops/platform');e=r/'docs/evidence/pre-sp07-20261007';p=pathlib.Path(__file__).parent;private=p/'terminal-pod-cleanup-r107';private.mkdir(mode=0o700,exist_ok=False)
def run(args):return subprocess.check_output(['kubectl','--context','orbstack',*args],stderr=subprocess.PIPE)
def get(kind,ns=None):return json.loads(run(['get',kind,*(['-n',ns]if ns else['-A']),'-o','json']))
def savepriv(name,data):
 f=private/name;f.write_bytes(data);f.chmod(0o600);return hashlib.sha256(data).hexdigest()
def record(name,value):(e/name).write_text(json.dumps(value,indent=2)+'\n')
nslist=get('namespaces');pods=get('pods');savepriv('before-pods.json',json.dumps(pods).encode());nsmap={x['metadata']['name']:x for x in nslist['items']};baseline=json.loads((r/'docs/evidence/pre-sp07-20261004/inventory-before/deployments.json').read_text());basemap={(x['metadata']['namespace'],x['metadata']['name']):x['metadata']['uid']for x in baseline['items']};cache={};selected=[];retained=[]
current={'ops-pre-sp07-core-r7-20261007','ops-pre-sp07-core-r8-20261007','ops-pre-sp07-targets-r8-20261007'}
for x in pods['items']:
 m=x['metadata'];ns=m['namespace'];phase=x['status']['phase'];row={'namespace':ns,'namespaceUID':nsmap[ns]['metadata']['uid'],'name':m['name'],'uid':m['uid'],'resourceVersion':m['resourceVersion'],'phase':phase,'statusReason':x['status'].get('reason'),'owners':m.get('ownerReferences',[])}
 reason=None;proof=None
 if phase not in ['Succeeded','Failed']:reason='current non-terminal Pod; retain'
 elif ns in current:reason='current acceptance/recovery target; retain'
 elif m.get('finalizers'):reason='finalizer protection; retain'
 elif any(any(k in vol for k in ['persistentVolumeClaim','hostPath','csi','ephemeral'])for vol in x['spec'].get('volumes',[])):reason='data or host storage exposure; retain'
 else:
  labels=m.get('labels',{});nlabels=nsmap[ns]['metadata'].get('labels',{});owners=m.get('ownerReferences',[])
  if nlabels.get('ops.platform.io/owner') in {'pre-sp07-20261004','pre-sp07-20261006-r6','pre-sp07-20261007-r7'} and labels.get('ops.platform.io/owner')==nlabels['ops.platform.io/owner'] and not owners:
   proof={'kind':'explicit namespace and Pod owner agreement','owner':labels['ops.platform.io/owner'],'purpose':labels.get('ops.platform.io/purpose')}
  elif len(owners)==1 and owners[0]['kind']=='ReplicaSet' and labels.get('ops.platform.io/release'):
   if ns not in cache:cache[ns]={(o['kind'],o['metadata']['name']):o for k in ['replicaset','deployment']for o in get(k,ns)['items']}
   ref=owners[0];rs=cache[ns].get(('ReplicaSet',ref['name']));deployrefs=rs['metadata'].get('ownerReferences',[])if rs else[]
   if rs and rs['metadata']['uid']==ref['uid'] and len(deployrefs)==1 and deployrefs[0]['kind']=='Deployment':
    dr=deployrefs[0];dep=cache[ns].get(('Deployment',dr['name']));ann=dep['metadata'].get('annotations',{})if dep else{}
    if dep and dep['metadata']['uid']==dr['uid'] and ann.get('meta.helm.sh/release-name')==labels['ops.platform.io/release'] and ann.get('meta.helm.sh/release-namespace')==ns and (nlabels.get('ops.platform.io/managed-by')=='opsctl-bootstrap' or basemap.get((ns,dr['name']))==dr['uid']):
     proof={'kind':'exact Pod -> ReplicaSet -> Deployment UID and Helm owner chain','replicaSetUID':ref['uid'],'deploymentUID':dr['uid'],'release':ann['meta.helm.sh/release-name'],'namespaceInstallationId':nlabels.get('ops.platform.io/installation-id'),'initialBaselineDeploymentUIDMatches':basemap.get((ns,dr['name']))==dr['uid']}
  if not proof:reason='ownership not established for this cleanup; retain'
 if proof:row['ownershipProof']=proof;selected.append((row,x))
 else:row['decision']=reason;retained.append(row)
# Preserve per-resource status/events and readable bounded logs before any deletion.
def capture(pair):
 row,x=pair;uid=row['uid'];status={'metadata':{k:x['metadata'].get(k)for k in ['name','namespace','uid','resourceVersion','creationTimestamp','ownerReferences','labels']},'status':x['status']};row['statusArchiveSHA256']=savepriv(uid+'-status.json',json.dumps(status).encode());events=run(['get','events','-n',row['namespace'],'--field-selector','involvedObject.uid='+uid,'-o','json']);row['eventArchiveSHA256']=savepriv(uid+'-events.json',events);logs=[]
 for c in x['spec']['containers']:
  cmd=['kubectl','--context','orbstack','logs','-n',row['namespace'],row['name'],'-c',c['name'],'--timestamps','--tail=1000','--limit-bytes=1048576','--request-timeout=5s'];v=subprocess.run(cmd,stdout=subprocess.PIPE,stderr=subprocess.PIPE);logs.append({'container':c['name'],'command':cmd,'exitCode':v.returncode,'stdoutSHA256':savepriv(uid+'-'+c['name']+'.log',v.stdout),'stderrSHA256':savepriv(uid+'-'+c['name']+'.stderr',v.stderr),'boundedTail':1000,'maximumBytes':1048576})
 row['logArchives']=logs;return row
with concurrent.futures.ThreadPoolExecutor(max_workers=6)as pool:rows=list(pool.map(capture,selected))
def protected():
 out={}
 for k in ['persistentvolumeclaims','persistentvolumes','secrets']:
  # Secrets payload never enters output or retained archive.
  vals=get(k);out[k]=sorted((a['metadata'].get('namespace',''),a['metadata']['name'],a['metadata']['uid'],a.get('spec',{}).get('volumeName'),json.dumps(a.get('spec',{}).get('claimRef'),sort_keys=True))for a in vals['items'])
 return out
before=protected();plan={'observedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'context':'orbstack','selectedCount':len(rows),'selected':rows,'retained':retained,'privateArchiveDirectory':str(private),'deletionScope':'terminal stateless owned Pod API objects only; no controllers, namespaces, volumes, keys, retention objects or finalizers','protectedResourceIdentitiesBefore':before};record('terminal-owned-pod-cleanup-plan-r107.json',plan);print('Archived and verified candidates',len(rows),'by namespace',dict(collections.Counter(x['namespace']for x in rows)),flush=True)
sock=socket.socket();sock.bind(('127.0.0.1',0));port=sock.getsockname()[1];sock.close();proxylog=(private/'proxy.log').open('wb');proxy=subprocess.Popen(['kubectl','--context','orbstack','proxy','--address=127.0.0.1','--port='+str(port),'--accept-hosts=^127\\.0\\.0\\.1$'],stdout=proxylog,stderr=subprocess.STDOUT);results=[]
try:
 origin='http://127.0.0.1:'+str(port);deadline=time.monotonic()+15
 while True:
  try:urllib.request.urlopen(origin+'/version',timeout=2).read();break
  except OSError:
   if time.monotonic()>deadline:raise
   time.sleep(.2)
 for row in rows:
  ns=row['namespace'];name=row['name'];live=json.loads(run(['get','pod',name,'-n',ns,'-o','json']));m=live['metadata'];assert m['uid']==row['uid'] and live['status']['phase']in ['Succeeded','Failed'] and m.get('ownerReferences',[])==row['owners'] and not m.get('finalizers');assert not any(any(k in vol for k in ['persistentVolumeClaim','hostPath','csi','ephemeral'])for vol in live['spec'].get('volumes',[]));liveNS=json.loads(run(['get','namespace',ns,'-o','json']));assert liveNS['metadata']['uid']==row['namespaceUID']
  body={'apiVersion':'v1','kind':'DeleteOptions','propagationPolicy':'Background','preconditions':{'uid':m['uid'],'resourceVersion':m['resourceVersion']}};url=origin+'/api/v1/namespaces/'+ns+'/pods/'+name;req=urllib.request.Request(url,data=json.dumps(body).encode(),method='DELETE',headers={'Content-Type':'application/json'});res=urllib.request.urlopen(req,timeout=10);res.read();results.append({'namespace':ns,'name':name,'uid':m['uid'],'httpStatus':res.status,'uidAndResourceVersionPreconditions':True});record('terminal-owned-pod-cleanup-operations-r107.json',results)
finally:proxy.terminate();proxy.wait(timeout=10);proxylog.close()
afterpods=get('pods');savepriv('after-pods.json',json.dumps(afterpods).encode());remaining={x['metadata']['uid']for x in afterpods['items']};deleted={x['uid']for x in results};assert not deleted.intersection(remaining);after=protected();assert before==after,'protected object UID/binding identity changed';report={'observedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'exitCode':0,'selectedCount':len(rows),'deletedCount':len(results),'deletedUIDsVerifiedAbsent':True,'retainedCount':len(retained),'protectedPVCsPVSecretUIDsAndClaimBindingsUnchanged':True,'controllersModified':False,'storageDataRemoved':False,'retentionChanged':False,'finalizersRemoved':False,'phaseCountsAfter':dict(collections.Counter(x['status']['phase']for x in afterpods['items'])),'fullOrbStackCleanupComplete':False,'fullReadinessAcceptance':False,'operationsEvidence':'terminal-owned-pod-cleanup-operations-r107.json','planEvidence':'terminal-owned-pod-cleanup-plan-r107.json'};record('terminal-owned-pod-cleanup-result-r107.json',report);print(json.dumps(report),flush=True)
