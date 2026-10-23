"""Forward only this new preview, retaining all previous artifacts and containers."""
from pathlib import Path
import json
import shutil
import subprocess
import tarfile

r = Path('/data/ops-ui-v12-mfa-20261022-r1/gpt-resume-r13')
p = r / 'preview'
assert (r / 'http-password-error-final.log').read_text().rstrip().endswith('\nPASS')
old = json.loads(subprocess.check_output(['docker', 'inspect', 'ops-ui-v12-gpt-r13-web-r2']))[0]
assert old['Config']['Labels']['ops.local/task'] == 'ui-v12-mfa-gpt-20261022'
assert any(m['Source'] == str(p / 'web') for m in old['Mounts'])
web = p / 'web-password'
web.mkdir(mode=0o755)
with tarfile.open(r / 'gpt-resume-r13-web-password.tgz') as archive:
    archive.extractall(web, filter='data')
(web / 'runtime-config.json').write_text(json.dumps({'auth': {'mode': 'native'}}))
shutil.copyfile(r / 'platform-api-gpt-resume-r13-password-final', p / 'platform-api-password')
(p / 'platform-api-password').chmod(0o700)
backup = p / 'run-api-before-password.py'
assert not backup.exists()
shutil.copyfile(p / 'run-api.py', backup)
(p / 'run-api.py').write_text(backup.read_text().replace("'platform-api'", "'platform-api-password'"))
subprocess.run(['systemctl', 'restart', 'ops-ui-v12-gpt-r13-api.service'], check=True)
subprocess.run(['systemctl', 'is-active', 'ops-ui-v12-gpt-r13-api.service'], check=True)
subprocess.run(['docker', 'stop', 'ops-ui-v12-gpt-r13-web-r2'], check=True)
args = ['docker', 'run', '-d', '--name', 'ops-ui-v12-gpt-r13-web-r3', '--label', 'ops.local/task=ui-v12-mfa-gpt-20261022', '--user', '101:101', '--network=host', '--read-only', '--tmpfs', '/tmp', '--tmpfs', '/var/cache/nginx', '--tmpfs', '/var/run']
for source, dest in [(web, '/preview/web'), (p / 'tls', '/preview/tls'), (p / 'nginx.conf', '/etc/nginx/nginx.conf')]:
    args += ['--mount', 'type=bind,src=' + str(source) + ',dst=' + dest + ',readonly']
subprocess.run(args + ['--entrypoint', 'nginx', old['Image'], '-e', '/dev/stderr', '-c', '/etc/nginx/nginx.conf', '-g', 'daemon off;'], check=True)
