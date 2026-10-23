from pathlib import Path
import json,subprocess,tarfile
r=Path('/data/ops-ui-v12-mfa-20261022-r1/gpt-login-r15');p=r/'preview-r2'
old=json.loads(subprocess.check_output(['docker','inspect','ops-ui-v12-gpt-r15-web-r3']))[0]
assert old['Config']['Labels']['ops.local/task']=='ui-v12-mfa-gpt-20261022'
assert any(m['Source']==str(p/'web-final') for m in old['Mounts'])
web=p/'web-layout-final';assert not web.exists();web.mkdir(mode=0o755)
with tarfile.open(r/'web-layout-final.tgz') as a:a.extractall(web,filter='data')
(web/'runtime-config.json').write_text(json.dumps({'auth':{'mode':'native'}}))
subprocess.run(['docker','stop','ops-ui-v12-gpt-r15-web-r3'],check=True)
a=['docker','run','-d','--name','ops-ui-v12-gpt-r15-web-r4','--label','ops.local/task=ui-v12-mfa-gpt-20261022','--user','101:101','--network=host','--read-only','--tmpfs','/tmp','--tmpfs','/var/cache/nginx','--tmpfs','/var/run']
for src,dst in [(web,'/preview/web'),(p/'tls','/preview/tls'),(p/'nginx.conf','/etc/nginx/nginx.conf')]:a+=['--mount',f'type=bind,src={src},dst={dst},readonly']
subprocess.run(a+['--entrypoint','nginx',old['Image'],'-e','/dev/stderr','-c','/etc/nginx/nginx.conf','-g','daemon off;'],check=True)
print('only r15 web advanced to immutable new directory; prior containers retained')
