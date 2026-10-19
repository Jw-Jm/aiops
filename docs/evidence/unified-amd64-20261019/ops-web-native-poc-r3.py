from pathlib import Path
import hashlib,json,os,ssl,subprocess,time,urllib.request,urllib.error
base=Path('/opt/ops-verification-20261019-1a7c7da');out=base/'distribution';repo=base/'repo';task=out/'web-native-poc-r3';task.mkdir(mode=0o755,exist_ok=False)
import socket
sockets=[]; ports=[]
for i in range(3):
 s=socket.socket();s.bind(('127.0.0.1',0));sockets.append(s);ports.append(s.getsockname()[1])
listen_port,api_port,oidc_port=ports
for s in sockets:s.close()
tls=task/'tls';tls.mkdir(mode=0o755)
subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-sha256','-days','1','-subj','/CN=localhost','-addext','subjectAltName=DNS:localhost,IP:127.0.0.1','-keyout',str(tls/'web.key'),'-out',str(tls/'web.crt')],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,check=True)
for p in tls.iterdir():p.chmod(0o400);os.chown(p,101,101)
(tls/'oidc-ca.pem').write_bytes((tls/'web.crt').read_bytes());(tls/'oidc-ca.pem').chmod(0o444)
config=(repo/'deploy/charts/ops-platform/files/sp08-nginx.conf').read_text()
for name,value in {'OPS_API_UPSTREAM':'127.0.0.1','OPS_OIDC_UPSTREAM':'127.0.0.1','OPS_OIDC_HOST':'localhost','OPS_PUBLIC_ORIGIN':'https://localhost:18443'}.items():config=config.replace('${'+name+'}',value)
config=config.replace('listen 8443 ssl;', f'listen 127.0.0.1:{listen_port} ssl;').replace('http://127.0.0.1:8080;', f'http://127.0.0.1:{api_port};').replace('https://127.0.0.1:8443/', f'https://127.0.0.1:{oidc_port}/')
(task/'server.conf').write_text(config);(task/'server.conf').chmod(0o444)
(task/'runtime-config.json').write_text(json.dumps({'oidc':{'authority':'https://localhost:8443/realms/ops','clientId':'ops-web','proxyPath':'/oidc'}}));(task/'runtime-config.json').chmod(0o444)
name='ops-web-material-poc-20261019-r3'
args=['docker','run','-d','--name',name,'--pull=never','--read-only','--cap-drop=ALL','--security-opt=no-new-privileges','--tmpfs=/tmp:rw,uid=101,gid=101,mode=0700,size=32m','--network=host','--mount=type=bind,src='+str(tls)+',dst=/etc/ops-tls,readonly','--mount=type=bind,src='+str(task/'server.conf')+',dst=/etc/nginx/conf.d/server.conf,readonly','--mount=type=bind,src='+str(task/'runtime-config.json')+',dst=/usr/share/nginx/html/runtime-config.json,readonly','ops.local/verification-20261019/platform-web:1a7c7da']
container=subprocess.check_output(args,text=True,timeout=30).strip();result={'containerID':container,'scope':'isolated native image HTTPS/static/runtime-config and unavailable API proxy; not browser login or installation','tlsTrust':'temporary isolated PoC certificate explicitly trusted; hostname verified; not platform certificate','passed':False,'network':'host with explicit loopback-only bind and unused loopback upstream ports; no host network configuration changes'}
try:
 context=ssl.create_default_context(cafile=str(tls/'web.crt'));client=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPSHandler(context=context))
 def get(path):
  try:
   with client.open(f'https://localhost:{listen_port}'+path,timeout=4)as response:return response.status,response.read(1048576)
  except urllib.error.HTTPError as e:return e.code,e.read(65536)
 for i in range(5):
  try:status,health=get('/healthz');break
  except urllib.error.URLError:
   if i==4:raise
   time.sleep(.3)
 assert status==200 and health==b'ready'
 status,index=get('/');assert status==200 and b'<div id="root">' in index
 status,runtime=get('/runtime-config.json');assert status==200 and json.loads(runtime)['oidc']['clientId']=='ops-web'
 status,proxy=get('/api/v1/unavailable');assert status==503 and json.loads(proxy)['code']=='UPSTREAM_UNAVAILABLE'
 result.update(passed=True,checks={'httpsHealth':True,'staticReactEntry':True,'runtimeConfig':True,'apiUnavailable503':True})
finally:
 logs=subprocess.run(['docker','logs',container],capture_output=True,text=True,timeout=10);(out/'platform-web-poc-container.log').write_text(logs.stdout+logs.stderr)
 subprocess.run(['docker','rm','-f',container],stdout=subprocess.DEVNULL,check=True,timeout=20)
 (out/'platform-web-native-poc.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps(result))
