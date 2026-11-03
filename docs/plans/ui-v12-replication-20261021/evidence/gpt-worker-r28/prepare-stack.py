"""Task-only real model/PKI/business runtime; no shared object mutation."""
from pathlib import Path
import base64,datetime,hashlib,json,os,socket,ssl,subprocess,urllib.request
root=Path('/data/ops-v12-native-visual-r24-20261026');r=root/'r28-worker';ns='ops-native-v12-r24-20261026'
assert socket.gethostname()=='mssc' and r.stat().st_uid==0
assert json.loads((r/'policy-browser-result.json').read_text())['status']=='passed'
def run(args,**kw):return subprocess.run(args,check=True,text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE,**kw).stdout
def save(path,data,mode=0o644):
 fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL,mode)
 with os.fdopen(fd,'wb') as f:f.write(data if isinstance(data,bytes) else data.encode())
def apply(doc):
 assert doc['metadata']['namespace']==ns
 return run(['kubectl','create','-f','-'],input=json.dumps(doc))
host='ops-openbao.'+ns+'.svc.cluster.local';ip=run(['kubectl','-n',ns,'get','svc','ops-openbao','-o','jsonpath={.spec.clusterIP}']).strip();lookup=socket.getaddrinfo
socket.getaddrinfo=lambda name,*args,**kw:lookup(ip if name==host else name,*args,**kw)
context=ssl.create_default_context(cafile=str(root/'private/bao-ca.pem'))
for resource,name in [('ca/pem','workload-ca.pem'),('crl/pem','workload-crl.pem')]:
 with urllib.request.urlopen('https://'+host+':8200/v1/pki/'+resource,context=context,timeout=10) as response:save(r/name,response.read())
with urllib.request.urlopen('https://api-docs.deepseek.com/',timeout=30) as response:documentation=response.read(2*1024*1024)
save(r/'deepseek-documentation.html',documentation)
protocol=(root/'evidence/deepseek-protocol-r27.json').read_bytes();assert json.loads(protocol)['status']=='passed'
digest=lambda raw:'sha256:'+hashlib.sha256(raw).hexdigest()
authorization={'decision':'deepseek就当作是内部模型','scope':'user-approved trusted DeepSeek model in existing internal Worker flow; no extra external governance','endpoint':'https://api.deepseek.com/v1','model':'deepseek-flash','billing':'provider-metered; bounded by existing model request/input/output token budgets; currency is not calculated by this runtime','alias':'provider-managed; profile freezes route and declared model, not hosted weights','protocolEvidenceDigest':digest(protocol),'documentationDigest':digest(documentation),'checks':'Go modelprofile/app/contract checks and24 Python profile/runtime/provider/ledger/failure tests passed; real protocol probe passed'}
save(r/'model-runtime-authorization.json',json.dumps(authorization,ensure_ascii=False,indent=2))
profile={'schemaVersion':'assistant-model-profile/v2','version':'deepseek-trusted-r28/v2','modelID':'deepseek-flash','modelVersion':'provider-deepseek-v4.1-flash-1017','baseURL':'https://api.deepseek.com/v1','apiKeyRef':'secret://model/api-key','contextWindow':65536,'outputTokenLimit':4096,'tokenAccounting':'reserve-context-bound','costPolicy':'provider-metered-token-bounded','toolCalling':True,'structuredOutput':True,'taskKinds':['data_query'],'timeoutSeconds':120,'retries':0,'hosted':{'provider':'deepseek','protocol':'openai-chat-completions','revisionPolicy':'provider-managed-alias','documentationURL':'https://api-docs.deepseek.com/','documentationDigest':digest(documentation),'protocolEvidenceDigest':digest(protocol)},'evaluationVersion':'deepseek-protocol-r27/v1','evaluationDigest':digest(protocol),'admissionEvidenceDigest':digest((r/'model-runtime-authorization.json').read_bytes()),'admissionState':'admitted'}
save(r/'model-profile.json',json.dumps(profile,indent=2))
reference=run([str(r/'material-2'),'reference']).strip();assert reference==profile['version']+'@sha256:'+hashlib.sha256(json.dumps(profile,sort_keys=True,separators=(',',':')).encode()).hexdigest()
tenant=json.loads((root/'evidence/first-tenant.json').read_text())['tenantId']
budget={'toolCalls':40,'rawQueries':10,'resultBytes':40<<20,'graphNodes':8000,'evidenceItems':4000,'modelRequests':8,'inputTokens':524288,'outputTokens':32768,'modelCostMicros':0,'durationSeconds':600,'allowedDataClasses':['D0','D1']}
sp06={'identityMode':'openbao-kubernetes','namespace':ns,'listenAddress':'0.0.0.0:18493','investigatorEndpoint':'https://ops-investigator.'+ns+'.svc.cluster.local:8086','certificateFile':'/etc/r28/tls.crt','privateKeyFile':'/etc/r28/tls.key','caFile':'/etc/r28/workload-ca.pem','crlFile':'/etc/r28/workload-crl.pem','policyName':'r28-read-only','budget':budget,'tenants':[tenant],'assistant':{'enabled':True,'faultChainEnabled':False,'modelProfileVersion':reference,'profile':profile}}
sp04={'IdentityMode':'openbao-kubernetes','Namespace':ns,'ListenAddress':'0.0.0.0:18492','OwnerEndpoint':'https://127.0.0.1:18492','ServerName':'ops-worker.'+ns+'.svc.cluster.local','CertificateFile':'/etc/r28/tls.crt','PrivateKeyFile':'/etc/r28/tls.key','CAFile':'/etc/r28/workload-ca.pem','CRLFile':'/etc/r28/workload-crl.pem','ContextPrivateKeyFile':'/etc/r28-scope/scope-key','ContextPublicKeyFile':'/etc/r28/scope-public-key','ArchiveBackendLogicalID':'r28-task-archive','AllowedWorkerCIDRs':['127.0.0.1/32'],'Clusters':[],'Sources':[]}
investigator={'listenAddress':'0.0.0.0:18496','jobAPI':'https://ops-api.'+ns+'.svc.cluster.local:8083','mcpEndpoint':'https://ops-api.'+ns+'.svc.cluster.local:8083/mcp','apiServerName':'ops-api.'+ns+'.svc.cluster.local','caFile':'/etc/r28/workload-ca.pem','crlFile':'/etc/r28/workload-crl.pem','certificateFile':'/etc/r28/tls.crt','privateKeyFile':'/etc/r28/tls.key','identityMode':'openbao-kubernetes','workerIdentity':'spiffe://ops.local/ns/'+ns+'/sa/ops-worker','modelAPIKeyFile':'/run/model/api-key','assistantModelProfileVersion':reference,'assistantModelProfile':profile,'model':None}
public={'workload-ca.pem':(r/'workload-ca.pem').read_text(),'workload-crl.pem':(r/'workload-crl.pem').read_text(),'sp04.json':json.dumps(sp04),'sp06.json':json.dumps(sp06),'investigator.json':json.dumps(investigator)}
apply({'apiVersion':'v1','kind':'ConfigMap','metadata':{'name':'r28-runtime','namespace':ns},'data':public,'binaryData':{'scope-public-key':base64.b64encode((r/'scope-public-key').read_bytes()).decode()}})
apply({'apiVersion':'v1','kind':'Secret','metadata':{'name':'r28-api-scope','namespace':ns},'data':{'scope-key':base64.b64encode((r/'private/scope-key').read_bytes()).decode()}})
apply({'apiVersion':'v1','kind':'Secret','metadata':{'name':'r28-model','namespace':ns},'data':{'api-key':base64.b64encode((root/'private/deepseek-api-key').read_bytes()).decode()}})
for name,port,target in [('ops-api',8083,18493),('ops-worker',8082,18492),('ops-investigator',8086,18496)]:
 apply({'apiVersion':'v1','kind':'Service','metadata':{'name':name,'namespace':ns,'labels':{'ops.local/task':'ui-v12-native-visual-r24'}},'spec':{'selector':{'app':name},'ports':[{'port':port,'targetPort':target}]}})
apply({'apiVersion':'v1','kind':'ServiceAccount','metadata':{'name':'ops-investigator','namespace':ns},'automountServiceAccountToken':False})
for duty in ['api','worker']:
 p=json.loads((root/(duty+'-pod-final.json')).read_text());assert p['metadata']['namespace']==ns;p['metadata'].setdefault('labels',{})['app']='ops-'+duty;c=p['spec']['containers'][0];c['command']=[str(r/('ops-'+duty))]
 for v in p['spec']['volumes']:
  if v['name']=='binary':v['hostPath']['path']=c['command'][0]
  if v['name']=='trust':v['hostPath']['path']=str(r/'registry-trust.json')
 for mount in c['volumeMounts']:
  if mount['name']=='binary':mount['mountPath']=c['command'][0]
 c.setdefault('env',[]).extend([{'name':'SP04_RUNTIME_FILE','value':'/etc/r28/sp04.json'},{'name':'SP06_RUNTIME_FILE','value':'/etc/r28/sp06.json'}]);c['volumeMounts'].append({'name':'r28-runtime','mountPath':'/etc/r28','readOnly':True});p['spec']['volumes'].append({'name':'r28-runtime','configMap':{'name':'r28-runtime'}})
 if duty=='api':c['volumeMounts'].append({'name':'r28-scope','mountPath':'/etc/r28-scope','readOnly':True});p['spec']['volumes'].append({'name':'r28-scope','secret':{'secretName':'r28-api-scope'}})
 save(r/(duty+'-pod-business.json'),json.dumps(p,indent=2))
projected={'name':'projected','projected':{'defaultMode':0o444,'sources':[{'serviceAccountToken':{'path':'token','audience':'openbao','expirationSeconds':600}}]}}
pod={'apiVersion':'v1','kind':'Pod','metadata':{'name':'ops-investigator','namespace':ns,'labels':{'app':'ops-investigator','ops.local/task':'ui-v12-native-visual-r24'}},'spec':{'hostNetwork':True,'dnsPolicy':'ClusterFirstWithHostNet','serviceAccountName':'ops-investigator','automountServiceAccountToken':False,'securityContext':{'runAsNonRoot':True,'runAsUser':65532,'runAsGroup':65532},'containers':[{'name':'investigator','image':'ops.local/verification-20261019/holmesgpt@sha256:7ecb3550a6cbff97b243ffbad07f80b8f640c63ac9b9531ffec4e8f8b8655ae3','imagePullPolicy':'IfNotPresent','command':['python','-c','import json; from investigator.worker import serve; serve(json.load(open("/etc/r28/investigator.json")))'],'env':[{'name':'PYTHONPATH','value':'/opt/r28-python'},{'name':'PYTHONDONTWRITEBYTECODE','value':'1'},{'name':'OPENBAO_ADDR','value':'https://'+host+':8200'},{'name':'OPENBAO_CA_FILE','value':'/etc/ops/runtime/bao-ca.pem'},{'name':'OPENBAO_PROJECTED_TOKEN_FILE','value':'/var/run/ops/openbao/token'}],'securityContext':{'allowPrivilegeEscalation':False,'readOnlyRootFilesystem':True,'capabilities':{'drop':['ALL']}},'volumeMounts':[{'name':'source','mountPath':'/opt/r28-python','readOnly':True},{'name':'r28-runtime','mountPath':'/etc/r28','readOnly':True},{'name':'bao','mountPath':'/etc/ops/runtime','readOnly':True},{'name':'model','mountPath':'/run/model','readOnly':True},{'name':'projected','mountPath':'/var/run/ops/openbao','readOnly':True},{'name':'tmp','mountPath':'/tmp'}],'readinessProbe':{'tcpSocket':{'port':18496},'periodSeconds':2}}],'volumes':[{'name':'source','hostPath':{'path':str(r/'source-3/services/investigator/src'),'type':'Directory'}},{'name':'r28-runtime','configMap':{'name':'r28-runtime'}},{'name':'bao','configMap':{'name':'r24-public-runtime'}},{'name':'model','secret':{'secretName':'r28-model','defaultMode':0o444}},projected,{'name':'tmp','emptyDir':{}}]}}
save(r/'investigator-pod.json',json.dumps(pod,indent=2));apply(pod)
save(r/'stack-prepared.json',json.dumps({'status':'prepared','namespace':ns,'tenant':tenant,'profileReference':reference,'provider':'deepseek-flash','scope':'native audit data_query; actual business startup still to verify'},indent=2))
print('task runtime material prepared; credentials omitted')
