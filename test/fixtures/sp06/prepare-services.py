import subprocess as sp, tempfile, pathlib, os, json, ssl, urllib.request, secrets, shlex, time, argparse, re
parser=argparse.ArgumentParser()
parser.add_argument('--container-suffix',default='20261003')
parser.add_argument('--private-root',type=pathlib.Path,default=pathlib.Path('/tmp'))
args=parser.parse_args()
if not re.fullmatch(r'[a-z0-9-]{1,40}',args.container_suffix):raise ValueError('invalid owned fixture suffix')
fixture_suffix=args.container_suffix
args.private_root.mkdir(parents=True,exist_ok=True)
root=str(pathlib.Path(__file__).resolve().parents[3])
d=pathlib.Path(tempfile.mkdtemp(prefix='ops-sp06-services-'+args.container_suffix+'-',dir=args.private_root)); os.chmod(d,0o700)
def run(args): return sp.check_output(args,stderr=sp.PIPE,text=True).strip()
def write(name,value,mode=0o600): p=d/name;p.write_text(value);os.chmod(p,mode);return str(p)
def docker(name,image,port,args,mounts=[],envfile=None):
 name=name.removesuffix('20261003')+fixture_suffix
 cmd=['docker','run','-d','--name',name,'--label','ops.platform.test=sp06-20261003','-p','127.0.0.1::'+str(port)]
 for m in mounts: cmd+=['-v',m]
 if envfile:cmd+=['--env-file',envfile]
 cmd+=[image]+args; ident=run(cmd)
 bound=run(['docker','port',name,str(port)+'/tcp']).split(':')[-1]
 print(json.dumps({'name':name,'id':ident,'image':image,'port':bound}),flush=True)
 return bound
key=d/'tls.key';cert=d/'tls.crt'
run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-keyout',str(key),'-out',str(cert),'-days','2','-subj','/CN=localhost','-addext','subjectAltName=DNS:localhost,IP:127.0.0.1'])
os.chmod(key,0o600)
conf=write('bao.hcl','ui=false\nstorage "inmem" {}\nlistener "tcp" { address="0.0.0.0:8200" tls_cert_file="/cfg/tls.crt" tls_key_file="/cfg/tls.key" }\n')
baoport=docker('ops-sp06-openbao-20261003','ghcr.io/openbao/openbao@sha256:4ca9310dd2a50c746d4227f44058088ee0470a8470031ee3f09cc8b1a69dd7f6',8200,['server','-config=/cfg/bao.hcl'],[str(d)+':/cfg:ro'])
bao='https://127.0.0.1:'+baoport;ctx=ssl.create_default_context(cafile=str(cert))
def req(path,data):
 r=urllib.request.Request(bao+'/v1/'+path,json.dumps(data).encode(),method='PUT',headers={'Content-Type':'application/json'})
 return json.loads(urllib.request.urlopen(r,context=ctx,timeout=5).read())
for attempt in range(60):
 try: init=req('sys/init',{'secret_shares':1,'secret_threshold':1});break
 except Exception:time.sleep(.5)
else:raise RuntimeError('OpenBao readiness failed')
write('recovery.json',json.dumps(init));req('sys/unseal',{'key':init['keys'][0]})
access=secrets.token_hex(12);secret=secrets.token_urlsafe(32)
s3cfg=write('s3.json',json.dumps({'identities':[{'name':'sp03-review','credentials':[{'accessKey':access,'secretKey':secret}],'actions':['Admin','Read','Write','List','Tagging']}]}))
(d/'seaweed-data').mkdir()
s3port=docker('ops-sp06-seaweed-20261003','docker.io/chrislusf/seaweedfs@sha256:d4cf67729aa8777e1a43a5b61d72e5b96179e4b7bac9a221cb14cbc2036cb32e',8333,['server','-dir=/data','-s3','-s3.config=/cfg/s3.json','-master.volumeSizeLimitMB=128'],[s3cfg+':/cfg/s3.json:ro',str(d/'seaweed-data')+':/data'])
password=secrets.token_urlsafe(32)
envfile=write('keycloak.env','KC_BOOTSTRAP_ADMIN_USERNAME=admin\nKC_BOOTSTRAP_ADMIN_PASSWORD='+password+'\n')
kcport=docker('ops-sp06-keycloak-20261003','quay.io/keycloak/keycloak@sha256:1f91ac24e8d68b8189d5d53a8381464c1db0fcff479348d5de973a86b63d621c',8080,['start-dev','--import-realm','--http-enabled=true','--hostname-strict=false'],[root+'/deploy/keycloak/realm-ops.json:/opt/keycloak/data/import/realm-ops.json:ro'],envfile)
values={'SP03_TEST_DATABASE_URL':os.environ['SP03_TEST_DATABASE_URL'],'SP03_TEST_OPENBAO_URL':bao,'SP03_TEST_OPENBAO_TOKEN':init['root_token'],'SP03_TEST_OPENBAO_CA_FILE':str(cert),'SP03_TEST_S3_ENDPOINT':'http://127.0.0.1:'+s3port,'SP03_TEST_S3_ACCESS_KEY':access,'SP03_TEST_S3_SECRET_KEY':secret,'SP03_KEYCLOAK_TEST_ISSUER':'http://127.0.0.1:'+kcport+'/realms/ops','SP03_KEYCLOAK_TEST_ADMIN_USERNAME':'admin','SP03_KEYCLOAK_TEST_ADMIN_PASSWORD':password}
write('credentials.env',''.join('export '+k+'='+shlex.quote(v)+'\n' for k,v in values.items()))
write('directory.txt',str(d))
pathlib.Path('/tmp/ops-sp06-services-current-dir').write_text(str(d))
print('isolated_config_directory='+str(d),flush=True)
