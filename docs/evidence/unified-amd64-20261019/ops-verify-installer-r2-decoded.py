from pathlib import Path
import base64,hashlib,json,subprocess
p=Path('/opt/ops-verification-20261019-1a7c7da/distribution');b=p/'installer-bundle-r2';trust=Path('/opt/ops-amd64-final-20261010/bundle-trust.pem');old='/opt/ops-amd64-final-20261010/opsctl-signed-r12'
checks=[]
decoded=p/'installer-r2-decoded.sig';decoded.write_bytes(base64.b64decode((b/'bundle.lock.sig').read_text().strip(),validate=True))
commands=[('openssl-signature-decoded',['openssl','pkeyutl','-verify','-pubin','-inkey',str(trust),'-rawin','-in',str(b/'bundle.lock.json'),'-sigfile',str(decoded)]),('previous-trusted-installer',[old,'bundle','verify','--manifest',str(b/'bundle.lock.json'),'--signature',str(b/'bundle.lock.sig'),'--payload',str(b/'payload.tar.zst'),'--key',str(trust)])]
for name,command in commands:
 with(p/('tools-r2-'+name+'.log')).open('w')as log:
  r=subprocess.run(command,stdout=log,stderr=subprocess.STDOUT,timeout=180)
 checks.append({'check':name,'exitCode':r.returncode});print(name,r.returncode,flush=True)
 if r.returncode:break
m=json.loads((b/'bundle.lock.json').read_text());item=next(x for x in m['materials']if x['name']=='opsctl');actual='sha256:'+hashlib.file_digest((p/'opsctl-unified-amd64-20261019-r2').open('rb'),'sha256').hexdigest();checks.append({'check':'verified-inventory-versus-executable','pass':item['digest']==actual,'sha256':actual})
(p/'installer-r2-independent-verification.json').write_text(json.dumps({'bundleId':m['bundleId'],'checks':checks,'result':'PASS'if len(checks)==3 and all(x.get('exitCode',0)==0 and x.get('pass',True)for x in checks)else 'FAIL'},indent=2)+'\n')
