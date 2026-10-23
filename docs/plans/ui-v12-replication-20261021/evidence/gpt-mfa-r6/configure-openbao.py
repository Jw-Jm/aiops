from pathlib import Path
import json,ssl,urllib.request,time,base64
root=Path('/data/ops-ui-v12-mfa-20261022-r1/gpt-r6-openbao');private=root/'private'
ctx=ssl.create_default_context(cafile=str(root/'tls/ca.crt'))
def req(path,body=None,token=None):
 headers={'Content-Type':'application/json'}
 if token:headers['X-Vault-Token']=token
 request=urllib.request.Request('https://127.0.0.1:18206/v1/'+path,data=json.dumps(body).encode() if body is not None else None,headers=headers,method='POST' if body is not None else 'GET')
 with urllib.request.urlopen(request,context=ctx,timeout=10) as response:
  data=response.read();return json.loads(data) if data else {}
for attempt in range(5):
 try:health=req('sys/init');break
 except OSError:
  if attempt==4:raise RuntimeError('task TLS server unavailable')
  time.sleep(1)
if health.get('initialized'):
 init=json.loads((private/'initialization.json').read_text())
else:
 init=req('sys/init',{'secret_shares':1,'secret_threshold':1});(private/'initialization.json').write_text(json.dumps(init));(private/'initialization.json').chmod(0o600)
req('sys/unseal',{'key':init['keys'][0]});token=init['root_token']
for attempt in range(10):
 if req('sys/leader').get('is_self'):break
 if attempt==9:raise RuntimeError('task Raft leader not active')
 time.sleep(1)

req('sys/auth/kubernetes',{'type':'kubernetes'},token)
reviewer=(private/'reviewer.jwt').read_text().strip();projected=(private/'projected.jwt').read_text().strip()
claims=json.loads(base64.urlsafe_b64decode(projected.split('.')[1]+'==='))
req('auth/kubernetes/config',{'kubernetes_host':'https://192.168.0.63:6443','kubernetes_ca_cert':(root/'tls/kubernetes-ca.crt').read_text(),'token_reviewer_jwt':reviewer,'disable_local_ca_jwt':True,'disable_iss_validation':False,'issuer':claims['iss']},token)
req('sys/mounts/transit',{'type':'transit'},token)
req('transit/keys/ui-v12-mfa',{'type':'aes256-gcm96','exportable':False,'allow_plaintext_backup':False},token)
policy='path "transit/encrypt/ui-v12-mfa" { capabilities=["update"] }\npath "transit/decrypt/ui-v12-mfa" { capabilities=["update"] }'
req('sys/policies/acl/ui-v12-mfa',{'policy':policy},token)
req('auth/kubernetes/role/ops-api-workload',{'bound_service_account_names':['ops-api'],'bound_service_account_namespaces':['ops-ui-v12-mfa-20261022-gpt'],'audience':'openbao','token_policies':['ui-v12-mfa'],'token_ttl':'2m','token_max_ttl':'15m'},token)
login=req('auth/kubernetes/login',{'role':'ops-api-workload','jwt':projected})
assert login['auth']['client_token'] and login['auth']['lease_duration']<=900
try:req('auth/kubernetes/login',{'role':'ops-api-workload','jwt':(private/'wrong.jwt').read_text().strip()});raise AssertionError('wrong workload accepted')
except urllib.error.HTTPError as e:
 if e.code!=403:raise
(root/'evidence/setup-result.json').write_text(json.dumps({'status':'passed','namespace':'ops-ui-v12-mfa-20261022-gpt','origin':'https://127.0.0.1:18206','key':'ui-v12-mfa','checks':['TLS CA verified','actual Kubernetes tokenreview login for task ops-api','wrong serviceaccount403','Transit policy limited to task key encrypt/decrypt'],'credentialLifetime':'projected fixture expires after1h; runtime deployment must use rotating projection'},indent=2))
print('task Kubernetes authentication accepted; wrong SA denied; scoped Transit key ready')
