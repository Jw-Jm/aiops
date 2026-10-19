from pathlib import Path
import base64,hashlib,json,subprocess
p=Path('/opt/ops-verification-20261019-1a7c7da/distribution');b=p/'core-bundle-r2';trust=Path('/opt/ops-amd64-final-20261010/bundle-trust.pem');tools=json.loads((p/'installer-r2-independent-verification.json').read_text());assert tools['result']=='PASS'
expected=next(x['sha256']for x in tools['checks']if x['check']=='verified-inventory-versus-executable');binary=p/'opsctl-unified-amd64-20261019-r2';assert 'sha256:'+hashlib.file_digest(binary.open('rb'),'sha256').hexdigest()==expected
sig=p/'core-r2-decoded.sig';sig.write_bytes(base64.b64decode((b/'bundle.lock.sig').read_text().strip(),validate=True));checks=[]
for name,args in [('openssl-signature',['openssl','pkeyutl','-verify','-pubin','-inkey',str(trust),'-rawin','-in',str(b/'bundle.lock.json'),'-sigfile',str(sig)]),('authenticated-new-installer',[str(binary),'bundle','verify','--manifest',str(b/'bundle.lock.json'),'--signature',str(b/'bundle.lock.sig'),'--payload',str(b/'payload.tar.zst'),'--key',str(trust)])]:
 with(p/('core-r2-'+name+'.log')).open('w')as log:r=subprocess.run(args,stdout=log,stderr=subprocess.STDOUT,timeout=600)
 checks.append({'check':name,'exitCode':r.returncode});print(name,r.returncode,flush=True)
 if r.returncode:break
manifest=json.loads((b/'bundle.lock.json').read_text());report={'bundleId':manifest['bundleId'],'payloadDigest':manifest['payload']['digest'],'architecture':manifest['architecture'],'checks':checks,'installerTrustChain':'previous trusted installer verified tools bundle; executable digest rechecked before execution','result':'PASS'if len(checks)==2 and all(x['exitCode']==0 for x in checks)else 'FAIL','installationAcceptance':'NOT_RUN'}
(p/'core-r2-independent-verification.json').write_text(json.dumps(report,indent=2)+'\n')
