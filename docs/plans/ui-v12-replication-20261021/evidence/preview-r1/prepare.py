"""One-shot setup on mssc-direct; only the existing task-owned PG instance.

Copy platform-api, opsctl and web build into BASE before running. Secrets stay
in private/ on the remote host; this script never prints them.
"""
import json
import os
from pathlib import Path
import secrets
import shutil
import socket
import subprocess
import uuid
from urllib.parse import quote

BASE = Path('/data/ops-ui-v12-http-20261022-r1/preview-r1')
PG = 'ops-ui-v12-http-pg-20261022-r1'
ORIGIN = 'https://192.168.0.63:30462'
assert socket.gethostname() == 'mssc'
assert BASE.parent.stat().st_uid == 0
assert not (BASE / 'private').exists(), 'one-shot setup already started; inspect before continuing'
for port in (18062, 19062, 30462):
    with socket.socket() as s:
        s.bind(('127.0.0.1' if port != 30462 else '192.168.0.63', port))
for folder in ('private', 'logs', 'tls'):
    (BASE / folder).mkdir(mode=0o700)

def private(name, content):
    path = BASE / 'private' / name
    with open(path, 'x', opener=lambda p, f: os.open(p, f, 0o600)) as out:
        out.write(content)
    return path

def sql(statement):
    result = subprocess.run(['docker', 'exec', '-i', PG, 'psql', '-X', '-v',
                             'ON_ERROR_STOP=1', '-U', 'postgres', '-d',
                             'ui_v12_native_identity'], input=statement,
                            text=True, capture_output=True)
    if result.returncode:
        raise RuntimeError('isolated preview DB setup failed; inspect privately')

tenant = str(uuid.uuid4())
api_password, bootstrap_password = secrets.token_urlsafe(36), secrets.token_urlsafe(36)
api_role, bootstrap_role = 'ui_v12_preview_r1_api', 'ui_v12_preview_r1_bootstrap'
sql(f"""BEGIN;
CREATE ROLE {api_role} LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD '{api_password}';
GRANT api_runtime_role TO {api_role};
CREATE ROLE {bootstrap_role} LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD '{bootstrap_password}';
GRANT migration_role TO {bootstrap_role};
SET LOCAL ROLE schema_owner;
INSERT INTO platform.tenants(tenant_id,slug,display_name)
VALUES('{tenant}', 'ui-v12-preview-20261022-r1', 'V1.2 独立开发验收');
COMMIT;
""")
def dsn(role, password):
    return f'postgres://{role}:{quote(password)}@127.0.0.1:15452/ui_v12_native_identity?sslmode=disable'
private('api.dsn', dsn(api_role, api_password))
private('administrator-password', secrets.token_urlsafe(24))
private('bootstrap.json', json.dumps(dict(databaseUrl=dsn(bootstrap_role, bootstrap_password),
        tenantId=tenant, loginName='v12.admin', displayName='V1.2 验收管理员')))
with (BASE / 'logs/bootstrap.json').open('x') as log:
    subprocess.run([str(BASE / 'opsctl'), 'bootstrap', 'native-administrator',
                    '--input', str(BASE / 'private/bootstrap.json'),
                    '--password-file', str(BASE / 'private/administrator-password')],
                   check=True, stdout=log)
shutil.copyfile('/opt/ops-verification-20261019-1a7c7da/install-r10/resolved-profile.yaml',
                BASE / 'resolved-profile.yaml')
(BASE / 'web/runtime-config.json').write_text(json.dumps({'auth': {'mode': 'native'}}))
subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:3072', '-nodes',
                '-keyout', str(BASE / 'tls/web.key'), '-out', str(BASE / 'tls/web.crt'),
                '-days', '30', '-subj', '/CN=ops-ui-v12-preview-r1',
                '-addext', 'subjectAltName=IP:192.168.0.63,IP:10.10.10.2'],
               check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
os.chmod(BASE / 'tls/web.key', 0o600)
for path in (BASE / 'tls', BASE / 'tls/web.key', BASE / 'tls/web.crt'):
    os.chown(path, 101, 101)  # Existing Web image runs as 101:101.
(BASE / 'run-api.py').write_text('''import os
from pathlib import Path
b=Path(__file__).resolve().parent
env=dict(os.environ)
env.update(DATABASE_URL=(b/'private/api.dsn').read_text(), HUMAN_AUTH_MODE='native',
 NATIVE_AUTH_TENANT_ID=''' + repr(tenant) + ''', NATIVE_AUTH_PUBLIC_ORIGIN=''' + repr(ORIGIN) + ''',
 PLATFORM_PROFILE=str(b/'resolved-profile.yaml'), PLATFORM_API_ADDR='127.0.0.1:18062',
 PLATFORM_METRICS_ADDR='127.0.0.1:19062')
os.execve(str(b/'platform-api'), [str(b/'platform-api')], env)
''')
(BASE / 'nginx.conf').write_text('''pid /tmp/nginx.pid;
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
  listen 192.168.0.63:30462 ssl;
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
   proxy_pass http://127.0.0.1:18062;
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
''')
(BASE / 'environment.json').write_text(json.dumps(dict(task='ui-v12-preview-20261022-r1',
    origin=ORIGIN, tenantId=tenant, login='v12.admin', databasePort=15452,
    apiPort=18062, metricsPort=19062, schemaVersion=46,
    scope='native login preview; no SP04/06/07 runtime or Worker configured',
    profileSource='/opt/ops-verification-20261019-1a7c7da/install-r10/resolved-profile.yaml'), indent=2))
print('Preview preparation complete; credentials stored only in remote private directory.')
