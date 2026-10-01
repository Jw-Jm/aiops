import pathlib,subprocess,json,ssl,urllib.request,os,base64,shlex,sys
base=pathlib.Path('/tmp/sp03-review-20261001-66yogqy9');tokenDir=base/'projected-tokens';tokenDir.mkdir(mode=0o700,exist_ok=True)
context=ssl.create_default_context(cafile=str(base/'tls.crt'))
def call(path,payload):
 req=urllib.request.Request('https://127.0.0.1:19420'+path,data=json.dumps(payload).encode(),headers={'Content-Type':'application/json'})
 with urllib.request.urlopen(req,context=context,timeout=10) as r:return r.status,json.load(r)
f=base/'kubernetes-bao-recovery.json'
if '--initialize' in sys.argv:
 status,recovery=call('/v1/sys/init',{'secret_shares':1,'secret_threshold':1})
 if f.exists():f.rename(base/'kubernetes-bao-recovery-first.json')
 f.write_text(json.dumps(recovery));f.chmod(0o600)
 status2,seal=call('/v1/sys/unseal',{'key':recovery['keys_base64'][0]})
 if seal['sealed']:raise SystemExit('isolated OpenBao remains sealed')
 print('OpenBao init HTTP',status,'unseal HTTP',status2,'sealed',seal['sealed'])
else:recovery=json.loads(f.read_text())
identities=[]
for filename,ns,pod,path in [*((sa,'ops-sp03-review-20261001','identity-'+sa,'/identity/token') for sa in ['ops-api','ops-worker','ops-investigator','ops-command-runner','ops-unauthorized']),('other-namespace','ops-sp03-review-20261001-other','identity-ops-api','/identity/token'),('wrong-audience','ops-sp03-review-20261001','identity-ops-api','/identity/wrong-audience')]:
 r=subprocess.run(['kubectl','exec','--namespace',ns,pod,'--','cat',path],capture_output=True,check=True)
 f=tokenDir/filename;f.write_bytes(r.stdout);f.chmod(0o600)
 part=r.stdout.decode().strip().split('.')[1];claims=json.loads(base64.urlsafe_b64decode(part+'='*(-len(part)%4)))
 identities.append({'file':filename,'namespace':ns,'pod':pod,'subject':claims['sub'],'audience':claims['aud'],'issuer':claims['iss'],'expiresAtUnix':claims['exp'],'podUid':claims['kubernetes.io']['pod']['uid']})
vals={'SP03_TEST_WORKLOAD_OPENBAO_URL':'https://127.0.0.1:19420','SP03_TEST_WORKLOAD_OPENBAO_TOKEN':recovery['root_token'],'SP03_TEST_WORKLOAD_OPENBAO_CA_FILE':str(base/'tls.crt'),'SP03_TEST_KUBERNETES_TOKEN_DIR':str(tokenDir)}
f=base/'workload.env';f.write_text(''.join('export '+k+'='+shlex.quote(v)+'\n' for k,v in vals.items()));f.chmod(0o600)
pathlib.Path('/tmp/sp03-review-authorized-token-identities.json').write_text(json.dumps(identities,indent=2)+'\n')
print('Actual Pod tokens refreshed outside Git:',len(identities))
