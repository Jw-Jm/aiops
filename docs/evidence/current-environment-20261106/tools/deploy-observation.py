from pathlib import Path
import subprocess,json,secrets,base64,os,shutil,copy,datetime
r=Path('/data/ops-current');rel=r/'releases/9372674a87201a4ccaab6737a1019808ea41ffd5';ns='ops-current';old='ops-native-v12-r24-20261026';tenant=json.loads((r/'evidence/first-tenant.json').read_text())['tenantId'];cluster=json.loads((r/'evidence/cluster-registration.json').read_text())['body']['data']['clusterUid']
def run(a,**kw):return subprocess.check_output(a,stderr=subprocess.PIPE,text=True,**kw)
def kube(*a):return json.loads(run(['kubectl',*a,'-o','json']))
def apply(d):run(['kubectl','apply','-f','-'],input=json.dumps(d))
def secure(name,data):
 p=r/'private'/name
 if not p.exists():p.write_text(json.dumps(data));p.chmod(0o600)
 return json.loads(p.read_text())
def certificate(kind):
 p=r/'private'/kind;p.mkdir(exist_ok=True);p.chmod(0o700)
 if not(p/'tls.key').exists():
  run(['openssl','req','-x509','-newkey','ec','-pkeyopt','ec_paramgen_curve:P-256','-nodes','-days','365','-subj','/CN=ops-current-'+kind,'-addext','subjectAltName=IP:127.0.0.1,DNS:localhost','-addext','basicConstraints=critical,CA:TRUE','-addext','keyUsage=critical,digitalSignature,keyCertSign','-keyout',str(p/'tls.key'),'-out',str(p/'tls.crt')])
 password=secure(kind+'-http.json',{'schemaVersion':'ops-source-http-credentials/v1','mode':'basic','username':'current-'+kind,'password':secrets.token_hex(32)})
 (p/'http-password').write_text(password['password'])
 for f in p.iterdir():f.chmod(0o600)
 return p,password
mp,mcred=certificate('metrics');lp,lcred=certificate('logs')
# Namespace metadata and kubelet endpoints are read-only; no command privileges.
for name in ['ops-metrics','ops-ksm','ops-log-collector']:
 apply({'apiVersion':'v1','kind':'ServiceAccount','metadata':{'name':name,'namespace':ns},'automountServiceAccountToken':False})
 apply({'apiVersion':'rbac.authorization.k8s.io/v1','kind':'ClusterRoleBinding','metadata':{'name':'ops-current-'+name+'-read'},'roleRef':{'apiGroup':'rbac.authorization.k8s.io','kind':'ClusterRole','name':'ops-current-readonly'},'subjects':[{'kind':'ServiceAccount','name':name,'namespace':ns}]})
cm=kube('get','cm','ops-r51-scrape','-n',old);scrape=cm['data']['scrape.json'].replace(old,ns).replace('01a177f1-372f-75b7-be90-e8121d1445f5',tenant).replace('r51-','current-').replace('/etc/r51/','/etc/metrics/').replace('19809','19819')
apply({'apiVersion':'v1','kind':'ConfigMap','metadata':{'name':'ops-metrics-scrape','namespace':ns},'data':{**cm['data'],'scrape.json':scrape}})
apply({'apiVersion':'v1','kind':'Secret','metadata':{'name':'ops-metrics-tls','namespace':ns},'stringData':{'http-password':mcred['password'],'tls.crt':(mp/'tls.crt').read_text(),'tls.key':(mp/'tls.key').read_text()}})
for kind,private,credential in [('metrics',mp,mcred),('logs',lp,lcred)]:
 apply({'apiVersion':'v1','kind':'Secret','metadata':{'name':'ops-'+kind+'-source','namespace':ns},'stringData':{'ca.crt':(private/'tls.crt').read_text(),'http.json':json.dumps(credential)}})
for previous,name in [('ops-r51-ksm','ops-ksm'),('ops-r51-metrics','ops-metrics')]:
 spec=kube('get','pod',previous,'-n',old)['spec'];spec['serviceAccountName']=name;spec['serviceAccount']=name
 for v in spec['volumes']:
  if 'hostPath'in v:
   data=r/'data/metrics';data.mkdir(exist_ok=True);os.chown(data,65534,65534);data.chmod(0o770);v['hostPath']['path']=str(data)
  if 'configMap'in v:v['configMap']['name']='ops-metrics-scrape'
  if 'secret'in v:v['secret']['secretName']='ops-metrics-tls'
 for c in spec['containers']:
  c['args']=[a.replace(old,ns).replace('19448','19548').replace('19809','19819').replace('19810','19820').replace('r51-metrics','current-metrics').replace('/etc/r51/','/etc/metrics/') for a in c.get('args',[])]
  for m in c.get('volumeMounts',[]):m['mountPath']=m['mountPath'].replace('/etc/r51','/etc/metrics')
 apply({'apiVersion':'v1','kind':'Pod','metadata':{'name':name,'namespace':ns,'labels':{'app':name}},'spec':spec})
# A new log store has its own data and credentials; it does not query the old store.
logsdata=r/'data/logs';logsdata.mkdir(exist_ok=True)
if not run(['docker','ps','-aq','--filter','name=^/ops-current-logs$']).strip():
 image=json.loads(run(['docker','image','inspect','ops.local/amd64-poc/victoria-logs:v1.52.0']))[0]['Id']
 run(['docker','run','-d','--name','ops-current-logs','--restart','unless-stopped','--pull=never','--read-only','--cap-drop=ALL','--security-opt=no-new-privileges','--pids-limit','64','--memory','512m','--cpus','1','--label','ops.platform.io/environment=current','-p','127.0.0.1:19538:9428','--mount',f'type=bind,source={lp},target=/private,readonly','--mount',f'type=bind,source={logsdata},target=/victoria-logs-data',image,'-httpListenAddr=:9428','-tls','-tlsCertFile=/private/tls.crt','-tlsKeyFile=/private/tls.key','-httpAuth.username=current-logs','-httpAuth.password=file:///private/http-password','-retentionPeriod=30d'])
# Preserve the admitted collector binary in the pinned release, not the legacy tree.
(rel/'collector').mkdir(exist_ok=True);shutil.copy2('/data/ops-v12-native-visual-r24-20261026/r49/bin/vlagent',rel/'collector/vlagent');(rel/'collector/vlagent').chmod(0o555)
ds=kube('get','ds','ops-r49-log-collector','-n',old);spec=ds['spec']['template']['spec'];name='ops-log-collector';spec['serviceAccountName']=name;spec['serviceAccount']=name
apply({'apiVersion':'v1','kind':'Secret','metadata':{'name':name,'namespace':ns},'stringData':{'http-password':lcred['password'],'tls.crt':(lp/'tls.crt').read_text()}})
state=r/'data/log-collector';state.mkdir(exist_ok=True)
for v in spec['volumes']:
 if v['name']=='bin':v['hostPath']['path']=str(rel/'collector')
 elif v['name']=='state':v['hostPath']['path']=str(state)
 elif v['name']=='private':v['secret']['secretName']=name
for c in spec['containers']:
 c['args']=[a.replace(old,ns).replace('19429','19529').replace('19438','19538').replace('r49-archive','current-logs').replace('ops-r49-log-collector','ops-log-collector').replace('01a177f1-372f-75b7-be90-e8121d1445f5',tenant) for a in c['args']]
 apply({'apiVersion':'apps/v1','kind':'DaemonSet','metadata':{'name':name,'namespace':ns},'spec':{'selector':{'matchLabels':{'app':name}},'template':{'metadata':{'labels':{'app':name}},'spec':spec}}})
receipt={'startedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'tenantId':tenant,'clusterUid':cluster,'namespace':ns,'metricsEndpoint':'https://127.0.0.1:19548','logsEndpoint':'https://127.0.0.1:19538','collectorVersion':'v1.52.0-ops1','metricsCollector':'victoriametrics-kubernetes/v1','coverageComplete':False}
(r/'evidence/observation-start.json').write_text(json.dumps(receipt,indent=2)+'\n');print('Independent current metrics, KSM, log store and scoped collector deployed')
