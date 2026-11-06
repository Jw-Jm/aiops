from pathlib import Path
import subprocess,json,os,argparse
r=Path('/data/ops-current');rel=r/'releases/9372674a87201a4ccaab6737a1019808ea41ffd5';p=r/'private/web-tls';p.mkdir(mode=0o700,exist_ok=True)
a=argparse.ArgumentParser();a.add_argument('--port',type=int,default=30473);a.add_argument('--name',default='ops-current-web-candidate');args=a.parse_args()
if not(p/'web.key').exists():
 subprocess.run(['openssl','req','-x509','-newkey','rsa:3072','-nodes','-days','365','-subj','/CN=ops-current-web','-keyout',str(p/'web.key'),'-out',str(p/'web.crt'),'-addext','subjectAltName=IP:192.168.0.63,DNS:localhost','-addext','basicConstraints=critical,CA:TRUE','-addext','keyUsage=critical,digitalSignature,keyEncipherment,keyCertSign'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
for f in p.iterdir():os.chown(f,101,101);f.chmod(0o600 if f.name.endswith('.key') else 0o644)
os.chown(p,101,101);p.chmod(0o700)
(rel/'web/runtime-config.json').write_text(json.dumps({'auth':{'mode':'native'}}))
conf=r/('config/nginx-'+str(args.port)+'.conf')
conf.write_text('''pid /tmp/nginx.pid;
events {}
http {
 client_body_temp_path /tmp/client_temp;
 proxy_temp_path /tmp/proxy_temp;
 fastcgi_temp_path /tmp/fastcgi_temp;
 uwsgi_temp_path /tmp/uwsgi_temp;
 scgi_temp_path /tmp/scgi_temp;
 include /etc/nginx/mime.types;
 default_type application/octet-stream;
 access_log off;
 server {
  listen 192.168.0.63:PORT ssl;
  ssl_certificate /preview/tls/web.crt;
  ssl_certificate_key /preview/tls/web.key;
  ssl_protocols TLSv1.2 TLSv1.3;
  root /preview/web;
  server_tokens off;
  client_max_body_size 1m;
  add_header X-Content-Type-Options nosniff always;
  add_header Referrer-Policy no-referrer always;
  add_header X-Frame-Options SAMEORIGIN always;
  add_header Content-Security-Policy "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; frame-ancestors 'self'; base-uri 'self'; form-action 'self'" always;
  location /api/ {
   proxy_pass http://127.0.0.1:18592;
   proxy_http_version 1.1;
   proxy_set_header Host $http_host;
   proxy_set_header Connection '';
   proxy_buffering off;
   proxy_cache off;
   proxy_read_timeout 650s;
  }
  location = /runtime-config.json { expires -1; try_files $uri =404; }
  location / { try_files $uri $uri/ /index.html; }
 }
}
'''.replace('PORT',str(args.port)))
image=json.loads(subprocess.check_output(['docker','image','inspect','ops.local/verification-20261019/platform-web:1a7c7da']))[0]['Id']
cmd=['docker','run','-d','--name',args.name,'--pull=never','--network','host','--read-only','--user','101:101','--restart','unless-stopped','--label','ops.platform.io/environment=current','--label','ops.platform.io/git-commit='+rel.name,'--tmpfs','/tmp:rw,noexec,nosuid,size=64m,mode=1777','--cap-drop=ALL','--security-opt=no-new-privileges']
for source,dest in [(p,'/preview/tls'),(rel/'web','/preview/web'),(conf,'/etc/nginx/nginx.conf')]:cmd+=['--mount',f'type=bind,source={source},target={dest},readonly']
cmd+=['--entrypoint','/usr/sbin/nginx',image,'-e','/dev/stderr','-c','/etc/nginx/nginx.conf','-g','daemon off;'];subprocess.run(cmd,check=True,stdout=subprocess.DEVNULL)
(r/('evidence/web-'+str(args.port)+'.json')).write_text(json.dumps({'container':args.name,'imageID':image,'release':rel.name,'webRoot':str(rel/'web'),'url':f'https://192.168.0.63:{args.port}'},indent=2)+'\n');print('Current web started',args.port)
