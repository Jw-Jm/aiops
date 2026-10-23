# Run only via ssh mssc-direct. All objects and storage belong to this task.
from pathlib import Path
import json,subprocess,os,socket
root=Path('/data/ops-ui-v12-mfa-20261022-r1/gpt-r6-openbao')
namespace='ops-ui-v12-mfa-20261022-gpt'
def command(args,input=None):
 p=subprocess.run(args,input=input,text=True,capture_output=True)
 if p.returncode:raise RuntimeError('failed task setup command: '+args[0])
 return p.stdout
server=command(['kubectl','config','view','--minify','-o','jsonpath={.clusters[0].cluster.server}']).strip()
if server!='https://192.168.0.63:6443':raise RuntimeError('not approved development Kubernetes API')
with socket.socket() as s:
 if s.connect_ex(('127.0.0.1',18206))==0:raise RuntimeError('port already occupied; inspect before reuse')
if root.exists():raise RuntimeError('task directory exists; inspect before reuse')
root.mkdir(mode=0o700)
for name in ['private','tls','storage','evidence']:(root/name).mkdir(mode=0o700)
labels={'ops.local/task':'ui-v12-mfa-gpt-20261022'}
objects=[{'apiVersion':'v1','kind':'Namespace','metadata':{'name':namespace,'labels':labels}}]
for name in ['ops-api','bao-reviewer','wrong-workload']:objects.append({'apiVersion':'v1','kind':'ServiceAccount','metadata':{'name':name,'namespace':namespace,'labels':labels},'automountServiceAccountToken':False})
role='ops-ui-v12-mfa-20261022-tokenreview'
objects.extend([{'apiVersion':'rbac.authorization.k8s.io/v1','kind':'ClusterRole','metadata':{'name':role,'labels':labels},'rules':[{'apiGroups':['authentication.k8s.io'],'resources':['tokenreviews'],'verbs':['create']}]},{'apiVersion':'rbac.authorization.k8s.io/v1','kind':'ClusterRoleBinding','metadata':{'name':role,'labels':labels},'roleRef':{'apiGroup':'rbac.authorization.k8s.io','kind':'ClusterRole','name':role},'subjects':[{'kind':'ServiceAccount','namespace':namespace,'name':'bao-reviewer'}]}])
command(['kubectl','create','-f','-'],json.dumps({'apiVersion':'v1','kind':'List','items':objects}))
(root/'evidence/kubernetes-task-objects.json').write_text(json.dumps(objects,indent=2))
for sa,file,audience in [('bao-reviewer','reviewer.jwt',None),('ops-api','projected.jwt','openbao'),('wrong-workload','wrong.jwt','openbao')]:
 args=['kubectl','-n',namespace,'create','token',sa,'--duration=1h']
 if audience:args+=['--audience='+audience]
 (root/'private'/file).write_text(command(args).strip());(root/'private'/file).chmod(0o600)
config=json.loads(command(['kubectl','config','view','--minify','--raw','-o','json']))
import base64
(root/'tls/kubernetes-ca.crt').write_bytes(base64.b64decode(config['clusters'][0]['cluster']['certificate-authority-data']))
command(['openssl','req','-x509','-newkey','rsa:3072','-nodes','-days','7','-subj','/CN=UIV12 MFA task CA','-keyout',str(root/'private/ca.key'),'-addext','basicConstraints=critical,CA:TRUE','-addext','keyUsage=critical,keyCertSign,cRLSign','-addext','subjectKeyIdentifier=hash','-out',str(root/'tls/ca.crt')])
command(['openssl','req','-newkey','rsa:3072','-nodes','-subj','/CN=mfa-gpt-r6.ops.local','-keyout',str(root/'private/server.key'),'-out',str(root/'private/server.csr')])
(root/'private/server.ext').write_text('basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nsubjectAltName=DNS:mfa-gpt-r6.ops.local,IP:127.0.0.1\nextendedKeyUsage=serverAuth\nauthorityKeyIdentifier=keyid,issuer\n')
command(['openssl','x509','-req','-in',str(root/'private/server.csr'),'-CA',str(root/'tls/ca.crt'),'-CAkey',str(root/'private/ca.key'),'-CAcreateserial','-days','7','-extfile',str(root/'private/server.ext'),'-out',str(root/'tls/server.crt')])
for p in (root/'private').iterdir():p.chmod(0o600)
(root/'private/server.json').write_text(json.dumps({'storage':{'raft':{'path':'/task/storage','node_id':'ui-v12-mfa-r6'}},'listener':{'tcp':{'address':'0.0.0.0:8200','tls_cert_file':'/task/tls/server.crt','tls_key_file':'/task/private/server.key'}},'api_addr':'https://127.0.0.1:18206','cluster_addr':'https://127.0.0.1:8201','ui':False}))
volume='ops-ui-v12-mfa-gpt-r6-raft'
command(['docker','volume','create','--label','ops.local/task=ui-v12-mfa-gpt-20261022',volume])
command(['docker','run','-d','--name','ops-ui-v12-gpt-r6-openbao','--user','0:0','--cap-drop','ALL','-p','127.0.0.1:18206:8200','--mount','type=bind,src='+str(root)+',dst=/task','--mount','type=volume,src='+volume+',dst=/task/storage','--entrypoint','bao','ops.local/amd64-poc/openbao:2.7.0','server','-config=/task/private/server.json'])
print('isolated OpenBao TLS server started; new namespace and minimal tokenreview-only duty; no shared policies changed')
