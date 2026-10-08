"""Read-only public inventory. Never serializes Secret/ConfigMap values or process argv."""
import datetime, hashlib, json, pathlib, subprocess
ROOT = pathlib.Path(__file__).resolve().parent
OUT = ROOT / 'inventory-before'
OUT.mkdir(exist_ok=True)
receipts = []
def capture(args):
    p = subprocess.run(args, capture_output=True, text=True, timeout=60)
    receipts.append({'command': args, 'exitCode': p.returncode, 'stderr': p.stderr})
    if p.returncode:
        return None
    return json.loads(p.stdout) if p.stdout.strip() else None
def meta(x):
    m=x.get('metadata',{})
    return {k:m[k] for k in ['name','namespace','uid','resourceVersion','creationTimestamp','deletionTimestamp','labels','ownerReferences','finalizers'] if k in m} | {'helmOwner':{k:v for k,v in m.get('annotations',{}).items() if k in ['meta.helm.sh/release-name','meta.helm.sh/release-namespace','helm.sh/resource-policy']}}
def item(x):
    y={'apiVersion':x.get('apiVersion'), 'kind':x.get('kind'),'metadata':meta(x),'disposition':'ownership_pending_preserve','verification':'UID/owner/consumer and retention checks required before mutation'}
    kind=x.get('kind','')
    if kind in ['Secret','ConfigMap']:
        y['keys']=sorted(x.get('data',{})); y['type']=x.get('type')
    elif kind in ['PersistentVolume','PersistentVolumeClaim','StorageClass','APIService','Role','ClusterRole','RoleBinding','ClusterRoleBinding','Service','NetworkPolicy','Lease','Endpoints','EndpointSlice']:
        for k in ['spec','status','rules','subjects','roleRef','provisioner','parameters','reclaimPolicy','volumeBindingMode','endpoints','ports','subsets']:
            if k in x: y[k]=x[k]
    elif kind in ['Node','Pod','Deployment','StatefulSet','DaemonSet','ReplicaSet','Job','CronJob']:
        spec=x.get('spec',{}); pod=spec.get('template',{}).get('spec',spec)
        y['images']=[{'name':c.get('name'),'image':c.get('image'),'volumeMounts':c.get('volumeMounts',[]),'envReferences':[{'name':ev.get('name'),'valueFrom':ev.get('valueFrom')} for ev in c.get('env',[]) if 'valueFrom' in ev]} for c in pod.get('containers',[])+pod.get('initContainers',[])]
        y['volumes']=pod.get('volumes',[]); y['serviceAccountName']=pod.get('serviceAccountName'); y['nodeName']=pod.get('nodeName')
        y['status']=x.get('status',{})
        if kind=='Node': y['podCIDRs']=spec.get('podCIDRs')
    elif kind=='CustomResourceDefinition':
        y['definition']={k:x.get('spec',{}).get(k) for k in ['group','names','scope']}; y['versions']=[{'name':v['name'],'served':v['served'],'storage':v['storage']} for v in x.get('spec',{}).get('versions',[])]
    return y
resources=['namespaces','nodes','pods','deployments','statefulsets','daemonsets','replicasets','jobs','cronjobs','services','endpoints','endpointslices','secrets','configmaps','persistentvolumeclaims','persistentvolumes','storageclasses','roles','rolebindings','clusterroles','clusterrolebindings','customresourcedefinitions','apiservices','networkpolicies','leases']
for kind in resources:
    data=capture(['kubectl','--context','orbstack','get',kind,'-A','-o','json'])
    if data is not None: (OUT/(kind+'.json')).write_text(json.dumps({'items':[item(x) for x in data.get('items',[])]},indent=2)+'\n')
helm=capture(['helm','list','-A','--all','-o','json']); (OUT/'helm.json').write_text(json.dumps(helm,indent=2)+'\n')
ids=subprocess.check_output(['docker','ps','-aq'],text=True).split()
containers=[]
for i in range(0,len(ids),25):
    for c in capture(['docker','inspect',*ids[i:i+25]]) or []:
        containers.append({'id':c['Id'],'name':c['Name'],'imageID':c['Image'],'imageReference':c['Config']['Image'],'labels':c['Config'].get('Labels'),'mounts':c['Mounts'],'state':{k:v for k,v in c['State'].items() if k!='Error'},'networks':{k:{t:n.get(t) for t in ['NetworkID','IPAddress','GlobalIPv6Address','Gateway']} for k,n in c['NetworkSettings']['Networks'].items()},'disposition':'ownership_pending_preserve'})
(OUT/'docker-containers.json').write_text(json.dumps(containers,indent=2)+'\n')
volumes=subprocess.check_output(['docker','volume','ls','-q'],text=True).split(); vs=[]
for i in range(0,len(volumes),50):
    for v in capture(['docker','volume','inspect',*volumes[i:i+50]]) or []:
        vs.append({k:v[k] for k in ['Name','Driver','Labels','Mountpoint','Scope','CreatedAt','Options'] if k in v} | {'consumers':[c['id'] for c in containers if any(m.get('Name')==v['Name'] for m in c['mounts'])],'disposition':'ownership_pending_preserve'})
(OUT/'docker-volumes.json').write_text(json.dumps(vs,indent=2)+'\n')
images=capture(['docker','image','ls','--no-trunc','--digests','--format','json']) if False else subprocess.run(['docker','image','ls','--no-trunc','--digests','--format','json'],capture_output=True,text=True)
(OUT/'docker-images.jsonl').write_text(images.stdout)
p=subprocess.run(['ps','-axo','pid=,ppid=,comm='],capture_output=True,text=True); (OUT/'process-identities.txt').write_text(p.stdout)
p=subprocess.run(['lsof','-nP','-iTCP','-sTCP:LISTEN'],capture_output=True,text=True); (OUT/'listeners.txt').write_text(p.stdout)
(ROOT/'inventory-commands.json').write_text(json.dumps({'observedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'commands':receipts,'redaction':'No Secret/ConfigMap data values, container env/command or process argv serialized'},indent=2)+'\n')
print(json.dumps({'containers':len(containers),'volumes':len(vs),'commands':len(receipts),'failures':[r for r in receipts if r['exitCode']]}))
