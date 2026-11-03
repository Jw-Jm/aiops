"""Sample only task Pod projections; never persist JWTs or OpenBao tokens."""
from pathlib import Path
import base64, hashlib, json, socket, ssl, subprocess, time, urllib.error, urllib.request

root=Path('/data/ops-v12-native-visual-r24-20261026'); ns='ops-native-v12-r24-20261026'
host=f'ops-openbao.{ns}.svc.cluster.local'
ip=subprocess.check_output(['kubectl','-n',ns,'get','service','ops-openbao','-o','jsonpath={.spec.clusterIP}'],text=True).strip()
lookup=socket.getaddrinfo
socket.getaddrinfo=lambda name,*a,**kw:lookup(ip if name==host else name,*a,**kw)
ctx=ssl.create_default_context(cafile=str(root/'private/bao-ca.pem'))
def request(path, body=None, token=None):
    headers={'Content-Type':'application/json'}
    if token:headers['X-Vault-Token']=token
    req=urllib.request.Request(f'https://{host}:8200/v1/'+path,data=json.dumps(body).encode() if body is not None else None,headers=headers,method='POST' if body is not None else 'GET')
    try:
        with urllib.request.urlopen(req,context=ctx,timeout=10) as response:
            raw=response.read();return response.status,json.loads(raw) if raw else {}
    except urllib.error.HTTPError as e:return e.code,{}

sample={'time':time.strftime('%Y-%m-%dT%H:%M:%S%z'),'projections':{}}
for pod, volume in [('ops-api','projected'),('ops-worker','projected'),('ops-openbao-0','reviewer')]:
    obj=json.loads(subprocess.check_output(['kubectl','-n',ns,'get','pod',pod,'-o','json'],text=True))
    assert obj['metadata']['namespace']==ns
    path=Path('/data/kubelet/pods')/obj['metadata']['uid']/'volumes/kubernetes.io~projected'/volume/'token'
    jwt=(subprocess.check_output(['kubectl','-n',ns,'exec',pod,'--','cat','/var/run/secrets/kubernetes.io/serviceaccount/token'],text=True).strip() if pod=='ops-openbao-0' else path.read_text().strip());claims=json.loads(base64.urlsafe_b64decode(jwt.split('.')[1]+'==='))
    assert claims['sub'].startswith('system:serviceaccount:'+ns+':')
    item={'podUid':obj['metadata']['uid'],'sha256':hashlib.sha256(jwt.encode()).hexdigest(),'iat':claims['iat'],'exp':claims['exp'],'aud':claims['aud'],'subject':claims['sub']}
    if pod!='ops-openbao-0':
        status,reply=request('auth/kubernetes/login',{'role':pod+'-workload','jwt':jwt})
        assert status==200,(pod,'Kubernetes auth rejected',status)
        code,_=request('transit/encrypt/ops-native-mfa',{'plaintext':base64.b64encode(b'nonsecret rotation probe').decode()},reply['auth']['client_token'])
        assert code==(200 if pod=='ops-api' else 403),(pod,'MFA isolation failure',code)
        item.update(loginStatus=status,mfaEncryptStatus=code)
    sample['projections'][pod]=item
out=root/'evidence/projected-samples.json';samples=json.loads(out.read_text()) if out.exists() else []
samples.append(sample);out.write_text(json.dumps(samples,indent=2)+'\n')
print(json.dumps({'sample':len(samples),'statuses':{k:{a:v.get(a) for a in ['loginStatus','mfaEncryptStatus','iat']} for k,v in sample['projections'].items()}}))
