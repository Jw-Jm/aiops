"""Verify native identities/values and current stored sample times before registration."""
from pathlib import Path
import base64,datetime,hashlib,json,re,socket,ssl,subprocess,time,urllib.parse,urllib.request
assert socket.gethostname()=='mssc'
root=Path('/data/ops-current');w=root/'evidence/metrics';private=root/'private/metrics';s=json.loads((w/'source-start.json').read_text());ns=s['namespace']
def kube(*a):return subprocess.check_output(['kubectl',*a],stderr=subprocess.PIPE)
cred=json.loads((private/'source-http.json').read_text());headers={'Authorization':'Basic '+base64.b64encode((cred['username']+':'+cred['password']).encode()).decode()};ctx=ssl.create_default_context(cafile=str(private/'tls.crt'))
def query(q):
 req=urllib.request.Request(s['endpoint']+'/api/v1/query?'+urllib.parse.urlencode({'query':q,'nocache':'1'}),headers=headers)
 with urllib.request.urlopen(req,context=ctx,timeout=5)as r:
  d=json.load(r);assert d['status']=='success'and not d.get('isPartial')and not d.get('warnings');return d['data']['result']
meta=json.loads(kube('get','pod','ops-seaweedfs-0','-n',ns,'-o','json'));uid=meta['metadata']['uid'];container=meta['status']['containerStatuses'][0];selector='{tenant="'+s['tenantId']+'",cluster="'+s['clusterUid']+'",collector="victoriametrics-kubernetes/v1",namespace="'+ns+'",uid="'+uid+'"}'
rows=query('timestamp('+selector+') keep_metric_names');names={r['metric']['__name__']for r in rows};assert {'container_cpu_usage_seconds_total','container_memory_working_set_bytes','kube_pod_status_phase','kube_pod_container_status_restarts_total'}<=names
now=time.time();assert all(0<=now-float(r['value'][1])<45for r in rows)
for r in rows:
 assert r['metric']['pod']==meta['metadata']['name']
 if r['metric']['__name__'].startswith('container_'):
  assert uid.replace('-','_')in r['metric']['id']or uid in r['metric']['id']
  assert container['containerID'].split('://')[-1]in r['metric']['id']
restart=query('kube_pod_container_status_restarts_total'+selector);assert sum(float(r['value'][1])for r in restart)==sum(c['restartCount']for c in meta['status']['containerStatuses'])
phase=query('kube_pod_status_phase'+selector);assert [r['metric']['phase']for r in phase if float(r['value'][1])==1]==[meta['status']['phase']]
token=kube('create','token','ops-metrics','-n',ns,'--duration=600s').decode().strip()
req=urllib.request.Request('https://192.168.0.63:10250/metrics/cadvisor',headers={'Authorization':'Bearer '+token})
with urllib.request.urlopen(req,context=ssl.create_default_context(cafile='/etc/kubernetes/pki/ca.crt'),timeout=5)as r:raw=r.read(32<<20).decode()
lines=[line for line in raw.splitlines()if line.startswith(('container_cpu_usage_seconds_total{','container_memory_working_set_bytes{'))and ('namespace="'+ns+'"')in line and ('pod="'+meta['metadata']['name']+'"')in line and 'container="seaweedfs"'in line]
assert len(lines)==2 and all(uid.replace('-','_')in line or uid in line for line in lines)
for line in lines:assert float(line.split('} ')[1].split()[0])>0
assert not query('container_memory_working_set_bytes'+selector.replace(uid,'00000000-0000-4000-8000-000000000000'))
assert not query('{collector="victoriametrics-kubernetes/v1",namespace!="'+ns+'"}')
# Store only the source HTTP credential in the real task Bao.
host=f'ops-openbao.{ns}.svc.cluster.local';ip=kube('get','svc','ops-openbao','-n',ns,'-o','jsonpath={.spec.clusterIP}').decode();lookup=socket.getaddrinfo;socket.getaddrinfo=lambda name,*a,**k:lookup(ip if name==host else name,*a,**k)
baoToken=json.loads((root/'private/bao-recovery.json').read_text())['rootToken']
req=urllib.request.Request(f'https://{host}:8200/v1/secret/data/ops/current/metrics',data=json.dumps({'data':cred}).encode(),headers={'X-Vault-Token':baoToken,'Content-Type':'application/json'},method='POST')
with urllib.request.urlopen(req,context=ssl.create_default_context(cafile=str(root/'private/bao-ca.pem')),timeout=5)as r:assert r.status==200
proof={'checkedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'nativePodUID':uid,'namespace':ns,'nativeContainerID':container['containerID'],'metricFamilies':sorted(names),'seriesVerified':len(rows),'nativeCadvisorRows':len(lines),'nativeContainerIdentityMatched':True,'restartCountMatched':True,'phaseMatched':True,'allRawSampleAgesBelow45Seconds':True,'wrongUIDEmpty':True,'otherNamespaceEmpty':True,'coverageComplete':False}
(w/'source-qualification.json').write_text(json.dumps(proof,indent=2));cluster=json.loads((root/'evidence/cluster-registration.json').read_text())['body']['data'];(w/'registration-input.json').write_text(json.dumps({**s,'cluster':cluster}));print(json.dumps(proof))
