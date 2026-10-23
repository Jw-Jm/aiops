"""Separate preview for SL-005; preserve preceding preview and shared services."""
from pathlib import Path
import json, os, shutil, socket, subprocess, tarfile, hashlib
base=Path('/data/ops-ui-v12-mfa-20261022-r1')
r=base/'gpt-dialog-r14'; old=base/'gpt-resume-r13/preview'; p=r/'preview'
assert os.getuid()==0 and base.stat().st_uid==0
assert not p.exists()
for port in (30468,18088,19088):
    with socket.socket() as s:
        assert s.connect_ex(('192.168.0.63' if port==30468 else '127.0.0.1',port))!=0
image=json.loads(subprocess.check_output(['docker','inspect','ops-ui-v12-gpt-r13-web-r3']))[0]
assert image['Config']['Labels']['ops.local/task']=='ui-v12-mfa-gpt-20261022'
p.mkdir(mode=0o755)
for name in ('private','tls','web','logs'): (p/name).mkdir(mode=0o700)
for name in ('api.dsn','tenant-id','browser-identity.json'):
    shutil.copyfile(old/'private'/name,p/'private'/name); (p/'private'/name).chmod(0o600)
for name in ('web.crt','web.key'):
    shutil.copyfile(old/'tls'/name,p/'tls'/name); os.chown(p/'tls'/name,0,101); (p/'tls'/name).chmod(0o640)
os.chown(p/'tls',0,101); (p/'tls').chmod(0o750)
for name in ('resolved-profile.yaml','platform-api-password'):
    shutil.copyfile(old/name,p/name)
(p/'platform-api-password').chmod(0o700)
assert hashlib.sha256((old/'platform-api-password').read_bytes()).digest()==hashlib.sha256((p/'platform-api-password').read_bytes()).digest()
for name in ('run-api.py','nginx.conf'):
    (p/name).write_text((old/name).read_text().replace('30467','30468').replace('18087','18088').replace('19087','19088'))
with tarfile.open(r/'web.tgz') as a: a.extractall(p/'web',filter='data')
(p/'web').chmod(0o755); (p/'web/runtime-config.json').write_text(json.dumps({'auth':{'mode':'native'}}))
subprocess.run(['systemd-run','--unit=ops-ui-v12-gpt-r14-api','--property=Restart=on-failure','/usr/bin/python3',str(p/'run-api.py')],check=True)
a=['docker','run','-d','--name','ops-ui-v12-gpt-r14-web','--label','ops.local/task=ui-v12-mfa-gpt-20261022','--user','101:101','--network=host','--read-only','--tmpfs','/tmp','--tmpfs','/var/cache/nginx','--tmpfs','/var/run']
for src,dst in [('web','/preview/web'),('tls','/preview/tls'),('nginx.conf','/etc/nginx/nginx.conf')]:
    a+=['--mount',f'type=bind,src={p/src},dst={dst},readonly']
subprocess.run(a+['--entrypoint','nginx',image['Image'],'-e','/dev/stderr','-c','/etc/nginx/nginx.conf','-g','daemon off;'],check=True)
print('isolated r14 preview created; API binary unchanged; old preview preserved')
