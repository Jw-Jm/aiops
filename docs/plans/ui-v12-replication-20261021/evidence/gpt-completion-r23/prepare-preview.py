"""Create an isolated forward-only preview; never mutate the source database."""
import json
import os
from pathlib import Path
import secrets
import subprocess
from urllib.parse import quote, urlsplit, urlunsplit

root = Path('/data/ops-v12-completion-20261026-r1')
private = root / 'private'
private.mkdir(mode=0o700)
container = 'ops-ui-v12-mfa-pg-20261022'
database = 'ui_v12_r23_preview'
login = 'ui_v12_r23_api'
password = secrets.token_hex(32)

def sql(statement):
    return subprocess.run(['docker', 'exec', '-i', container, 'psql', '-X', '-U', 'postgres', '-d', 'postgres', '-v', 'ON_ERROR_STOP=1'], input=statement, text=True, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)

with (private / 'source-database.dump').open('wb') as output:
    subprocess.run(['docker', 'exec', container, 'pg_dump', '-U', 'postgres', '-d', 'ui_v12_native_identity_main_20261025', '-Fc'], stdout=output, check=True)
sql(f"CREATE DATABASE {database} OWNER ui_v12_native_owner;")
with (private / 'source-database.dump').open('rb') as source, (private / 'restore.log').open('wb') as log:
    subprocess.run(['docker', 'exec', '-i', container, 'pg_restore', '-U', 'postgres', '-d', database, '--exit-on-error'], stdin=source, stdout=log, stderr=subprocess.STDOUT, check=True)
sql(f"CREATE ROLE {login} LOGIN PASSWORD '{password}' NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS; GRANT api_runtime_role TO {login}; GRANT CONNECT ON DATABASE {database} TO {login};")
env = json.loads(Path('/opt/ops-main-deployment-20261025/candidate-env.json').read_text())
old = urlsplit(env['DATABASE_URL'])
owner_password = Path('/data/ops-ui-v12-mfa-20261022-r1/private/owner.password').read_text().strip()
migration_env = os.environ.copy()
migration_env['SP03_MIGRATION_DATABASE_URL'] = urlunsplit((old.scheme, 'ui_v12_native_owner:' + quote(owner_password, safe='') + '@' + old.netloc.split('@')[-1], '/' + database, old.query, ''))
with (root / 'evidence/preview-migration.log').open('w') as log:
    subprocess.run([str(root / 'db-migrate'), '-dir', str(root / 'source/migrations')], env=migration_env, stdout=log, stderr=subprocess.STDOUT, check=True)
env['DATABASE_URL'] = urlunsplit((old.scheme, login + ':' + password + '@' + old.netloc.split('@')[-1], '/' + database, old.query, ''))
env.update(NATIVE_AUTH_PUBLIC_ORIGIN='https://192.168.0.63:30470', PLATFORM_API_ADDR='127.0.0.1:18091', PLATFORM_METRICS_ADDR='127.0.0.1:19091')
for name in ('INVOCATION_ID', 'JOURNAL_STREAM', 'SYSTEMD_EXEC_PID', 'MEMORY_PRESSURE_WATCH', 'MEMORY_PRESSURE_WRITE'):
    env.pop(name, None)
(private / 'api-env.json').write_text(json.dumps(env))
(private / 'api-env.json').chmod(0o600)
config = Path('/data/ops-ui-v12-mfa-20261022-r1/gpt-login-r15/preview-r2/nginx.conf').read_text().replace(':30469', ':30470').replace(':18089', ':18091')
(root / 'nginx.conf').write_text(config)
print(json.dumps({'database': database, 'apiLogin': login, 'schema': 70, 'origin': env['NATIVE_AUTH_PUBLIC_ORIGIN'], 'sourceDatabaseUnchanged': True}))
