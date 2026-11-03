"""Renew the owned Bao CRL normally; keep signature and expiry checks enabled."""
from pathlib import Path
import datetime,json,socket,ssl,subprocess,urllib.request,tempfile,re
assert socket.gethostname()=='mssc'
base=Path('/data/ops-v12-native-visual-r24-20261026');work=base/'r48';ns='ops-native-v12-r24-20261026';host='ops-openbao.'+ns+'.svc.cluster.local'
ip=subprocess.check_output(['kubectl','-n',ns,'get','svc','ops-openbao','-o','jsonpath={.spec.clusterIP}'],text=True);lookup=socket.getaddrinfo;socket.getaddrinfo=lambda name,*a,**k:lookup(ip if name==host else name,*a,**k)
ctx=ssl.create_default_context(cafile=str(base/'private/bao-ca.pem'));token=json.loads((base/'private/bao-recovery.json').read_text())['rootToken']
def request(path,data=None):
 r=urllib.request.Request('https://'+host+':8200/v1/'+path,data=None if data is None else json.dumps(data).encode(),headers={'X-Vault-Token':token,'Content-Type':'application/json'})
 with urllib.request.urlopen(r,context=ctx,timeout=10) as response:return response.read()
def describe(raw):
 out=subprocess.check_output(['openssl','crl','-noout','-lastupdate','-nextupdate','-crlnumber'],input=raw).decode();text=subprocess.check_output(['openssl','crl','-noout','-text'],input=raw).decode()
 return {'dates':out.strip().splitlines(),'revokedEntries':len(re.findall(r'Serial Number:',text))}
before=describe(request('pki/crl/pem'));config=json.loads(request('pki/config/crl'))['data'];assert not config.get('disable')
# Use OpenBao's native renewal mechanism; retain its current expiry and revocation policy.
if not config.get('auto_rebuild'):request('pki/config/crl',{'auto_rebuild':True})
request('pki/crl/rotate');new=request('pki/crl/pem');after=describe(new);current=json.loads(request('pki/config/crl'))['data'];assert current['auto_rebuild'] and current['expiry']==config['expiry'] and not current.get('disable')
with tempfile.TemporaryDirectory(dir=work) as d:
 ca=Path(d)/'ca.pem';ca.write_bytes(request('pki/ca_chain'))
 subprocess.run(['openssl','crl','-noout','-verify','-CAfile',str(ca)],input=new,check=True,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE)
receipt={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'namespace':ns,'before':before,'after':after,'autoRebuildBefore':config.get('auto_rebuild',False),'autoRebuildAfter':True,'expiryUnchanged':current['expiry'],'signatureVerified':True,'checksDisabled':False,'noTidyOrRevocationDeletion':True,'documentation':'https://openbao.org/docs/commands/pki/health-check/'}
(work/'crl-repair.json').write_text(json.dumps(receipt,indent=2));print(json.dumps(receipt))
# Reset crash backoff only for the two owned r48 processes, using their current spec.
runtime=json.loads((work/'backend-runtime.json').read_text())
for artifact in runtime['artifacts']:
 name=artifact['component'];pod=json.loads(subprocess.check_output(['kubectl','-n',ns,'get','pod',name,'-o','json']));assert pod['spec']['containers'][0]['command'][0]==str(work/artifact['artifact'])
 manifest={'apiVersion':'v1','kind':'Pod','metadata':{'name':name,'namespace':ns,'labels':pod['metadata'].get('labels',{})},'spec':pod['spec']}
 artifact['crlStalePodUID']=pod['metadata']['uid']
 subprocess.run(['kubectl','-n',ns,'delete','pod',name,'--wait=true'],check=True,stdout=subprocess.DEVNULL)
 subprocess.run(['kubectl','create','-f','-'],input=json.dumps(manifest).encode(),check=True,stdout=subprocess.DEVNULL)
 subprocess.run(['kubectl','-n',ns,'wait','--for=condition=Ready','pod/'+name,'--timeout=120s'],check=True,stdout=subprocess.DEVNULL)
 actual=json.loads(subprocess.check_output(['kubectl','-n',ns,'get','pod',name,'-o','json']));artifact['podUID']=actual['metadata']['uid']
runtime['crlRepair']='crl-repair.json';runtime['recoveredAt']=datetime.datetime.now(datetime.timezone.utc).isoformat();(work/'backend-runtime-recovered.json').write_text(json.dumps(runtime,indent=2));print('owned API and Worker restarted after valid CRL renewal')
