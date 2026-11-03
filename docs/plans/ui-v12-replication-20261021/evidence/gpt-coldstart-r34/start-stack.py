"""Start native source canary; public manifests contain no credentials."""
from pathlib import Path
import hashlib
import json
import os
import shutil
import socket
import ssl
import subprocess
import urllib.request

assert socket.gethostname() == 'mssc'
root=Path('/data/ops-v12-native-cold-r34-20261027'); prior=Path('/data/ops-v12-native-visual-r24-20261026')
private=root/'private'; ns='ops-native-v12-r34-20261027'
def run(args, **kw):
    p=subprocess.run(args,text=True,capture_output=True,**kw)
    if p.returncode:
        f=private/('stack-failure-'+str(len(list(private.glob('stack-failure-*'))))+'.log')
        f.write_text(p.stdout+p.stderr);f.chmod(0o600)
        raise RuntimeError('command failed: '+args[0]+'; retained private log='+f.name)
    return p.stdout

def create(obj):return run(['kubectl','create','-f','-'],input=json.dumps(obj))
def get(kind,name,namespace=ns):return json.loads(run(['kubectl','get',kind,name,'-n',namespace,'-o','json']))

# Verify new archive with the production fresh-bucket initializer.
run(['kubectl','wait','-n',ns,'--for=condition=Ready','pod/ops-seaweedfs-0','--timeout=45s'])
ip=get('service','ops-seaweedfs-s3')['spec']['clusterIP']
host=f'ops-seaweedfs-s3.{ns}.svc.cluster.local'
env=dict(os.environ,S3_ENDPOINT=f'https://{ip}:8333',S3_SERVER_NAME=host)
(root/'evidence/archive-bucket.log').write_text(run([str(root/'ops-v12-r34-bucket')],env=env))
# Public pinned PKI material from this new OpenBao, never from the old stack.
host=f'ops-openbao.{ns}.svc.cluster.local'; ip=get('service','ops-openbao')['spec']['clusterIP']
lookup=socket.getaddrinfo
socket.getaddrinfo=lambda name,*a,**kw:lookup(ip if name==host else name,*a,**kw)
ctx=ssl.create_default_context(cafile=str(private/'bao-ca.pem'))
def public(path):
    with urllib.request.urlopen(f'https://{host}:8200/v1/'+path,context=ctx,timeout=15) as r:return r.read().decode()
ca=public('pki/ca/pem'); crl=public('pki/crl/pem')
# Raw Ed25519 seed/public formats match the existing Go scope signing contract.
seed=subprocess.run(['openssl','genpkey','-algorithm','ed25519','-outform','DER'],capture_output=True,check=True).stdout
pub=subprocess.run(['openssl','pkey','-inform','DER','-pubout','-outform','DER'],input=seed,capture_output=True,check=True).stdout[-32:]
key=seed[-32:]+pub
import base64
create({'apiVersion':'v1','kind':'Secret','metadata':{'name':'r34-scope','namespace':ns},'data':{'scope-key':base64.b64encode(key).decode()}})
reference=get('configmap','r28-runtime','ops-native-v12-r24-20261026')['data']
sp04=json.loads(reference['sp04.json']); sp04.update(Namespace=ns,ListenAddress='0.0.0.0:18410',OwnerEndpoint='https://127.0.0.1:18410',ServerName=f'ops-worker.{ns}.svc.cluster.local',Clusters=[],Sources=[],ArchiveBackendLogicalID='r34-task-archive')
for k in ['CertificateFile','PrivateKeyFile','CAFile','CRLFile','ContextPrivateKeyFile','ContextPublicKeyFile']:sp04[k]=sp04[k].replace('/etc/r28-scope','/etc/r34-scope').replace('/etc/r28','/etc/r34')
tenant=json.loads((root/'evidence/first-tenant.json').read_text())['tenantId']
sp06=json.loads(reference['sp06.json']);sp06.update(namespace=ns,listenAddress='0.0.0.0:18411',investigatorEndpoint='https://127.0.0.1:18412',tenants=[tenant],policyName='r34-read-only')
for k in ['certificateFile','privateKeyFile','caFile','crlFile']:sp06[k]=sp06[k].replace('/etc/r28','/etc/r34')
agent=json.loads(reference['investigator.json']);agent.update(listenAddress='0.0.0.0:18412',jobAPI='https://127.0.0.1:18411',mcpEndpoint='https://127.0.0.1:18411/mcp',apiServerName=f'ops-api.{ns}.svc.cluster.local',workerIdentity=f'spiffe://ops.local/ns/{ns}/sa/ops-worker')
for k in ['certificateFile','privateKeyFile','caFile','crlFile']:agent[k]=agent[k].replace('/etc/r28','/etc/r34')
# The frozen admitted model profile is reused verbatim. No model request or policy admission is fabricated.
assert agent['assistantModelProfile']==sp06['assistant']['profile']
create({'apiVersion':'v1','kind':'ConfigMap','metadata':{'name':'r34-runtime','namespace':ns},'data':{'sp04.json':json.dumps(sp04),'sp06.json':json.dumps(sp06),'investigator.json':json.dumps(agent),'workload-ca.pem':ca,'workload-crl.pem':crl,'scope-public-key':base64.b64encode(pub).decode()}})
# scope-public-key is a binary field, not base64 text in the mounted file.
cm=get('configmap','r34-runtime');cm['data'].pop('scope-public-key');cm['binaryData']={'scope-public-key':base64.b64encode(pub).decode()};run(['kubectl','replace','-f','-'],input=json.dumps(cm))
for duty in ['api','worker']:
    pod=json.loads((root/f'{duty}-pod.json').read_text())
    c=pod['spec']['containers'][0]
    c['env']=[{'name':'SP04_RUNTIME_FILE','value':'/etc/r34/sp04.json'},{'name':'SP06_RUNTIME_FILE','value':'/etc/r34/sp06.json'}]
    c['volumeMounts'].append({'name':'r34-runtime','mountPath':'/etc/r34','readOnly':True})
    pod['spec']['volumes'].append({'name':'r34-runtime','configMap':{'name':'r34-runtime'}})
    if duty=='api':
        c['volumeMounts'].append({'name':'scope','mountPath':'/etc/r34-scope','readOnly':True})
        pod['spec']['volumes'].append({'name':'scope','secret':{'secretName':'r34-scope','defaultMode':0o444}})
    (root/f'{duty}-pod-full.json').write_text(json.dumps(pod,indent=2))
    run(['kubectl','delete','pod','ops-'+duty,'-n',ns,'--wait=true','--timeout=40s'])
    create(pod)
create({'apiVersion':'v1','kind':'ServiceAccount','metadata':{'name':'ops-investigator','namespace':ns},'automountServiceAccountToken':False})
model=get('secret','r28-model','ops-native-v12-r24-20261026')
create({'apiVersion':'v1','kind':'Secret','metadata':{'name':'r34-model','namespace':ns},'data':model['data']})
pod=get('pod','ops-investigator','ops-native-v12-r24-20261026')
spec=pod['spec'];c=spec['containers'][0]
for e in c['env']:
    if e['name']=='OPENBAO_ADDR':e['value']=f'https://ops-openbao.{ns}.svc.cluster.local:8200'
c['command']=['python','-c','import json; from investigator.worker import serve; serve(json.load(open("/etc/r34/investigator.json")))']
for mount in c['volumeMounts']:
    if mount['mountPath']=='/etc/r28':mount['mountPath']='/etc/r34'
for v in spec['volumes']:
    if v['name']=='source':v['hostPath']['path']=str(root/'source/services/investigator/src')
    elif v['name']=='r28-runtime':v['configMap']['name']='r34-runtime'
    elif v['name']=='bao':v['configMap']['name']='r34-public-runtime'
    elif v['name']=='model':v['secret']['secretName']='r34-model'
pod={'apiVersion':'v1','kind':'Pod','metadata':{'name':'ops-investigator','namespace':ns,'labels':{'app':'ops-investigator'}},'spec':spec}
(root/'investigator-pod.json').write_text(json.dumps(pod,indent=2));create(pod)
# Current formal frontend bundle, separate origin/runtime config and new TLS key.
shutil.copytree(prior/'web-r14',root/'web')
runtime=json.loads((root/'web/runtime-config.json').read_text())
# Runtime schema carries native mode only; keep its existing contract unchanged.
assert runtime['auth']['mode']=='native'
(root/'web/runtime-config.json').write_text(json.dumps(runtime))
tls=root/'tls';tls.mkdir(mode=0o700)
run(['openssl','req','-x509','-newkey','rsa:3072','-nodes','-days','30','-subj','/CN=192.168.0.63','-addext','subjectAltName=IP:192.168.0.63','-keyout',str(tls/'web.key'),'-out',str(tls/'web.crt')])
user=run(['docker','inspect','--format','{{.Config.User}}','ops-v12-native-r30-web-r14']).strip();uid=int(user.split(':')[0]) if user else 0
os.chown(tls,uid,uid)
for f in tls.iterdir():os.chown(f,uid,uid);f.chmod(0o600)
nginx=(prior/'nginx.conf').read_text().replace('30471','30472').replace('18092','18094');(root/'nginx.conf').write_text(nginx)
image=run(['docker','inspect','--format','{{.Config.Image}}','ops-v12-native-r30-web-r14']).strip()
run(['docker','run','-d','--name','ops-v12-native-r34-web','--network','host','--restart','unless-stopped','--read-only','--tmpfs','/tmp:rw,noexec,nosuid,size=64m,mode=1777','--cap-drop','ALL','--security-opt','no-new-privileges', '-v',f'{root}/web:/preview/web:ro','-v',f'{root}/nginx.conf:/etc/nginx/nginx.conf:ro','-v',f'{tls}:/preview/tls:ro',image,'-c','/etc/nginx/nginx.conf','-g','daemon off;'])
(root/'evidence/stack-started.json').write_text(json.dumps({'namespace':ns,'database':'ui_v12_r34_native','tenantId':tenant,'origin':'https://192.168.0.63:30472','components':['Web','API','Worker','investigator','CLI','OpenBao','S3'],'oidc':False,'modelCalls':0,'formalBundleAcceptance':False,'webIndexSha256':hashlib.sha256((root/'web/index.html').read_bytes()).hexdigest()},indent=2))
print('isolated source canary started; readiness and business browser verification still required')
