"""Create a new task preview only after guarded integration passes."""
from pathlib import Path
import json
import shutil
import socket
import subprocess
import tarfile

task = Path('/data/ops-ui-v12-mfa-20261022-r1')
round_dir = task / 'gpt-resume-r13'
old = task / 'gpt-r6-preview'
preview = round_dir / 'preview'
assert (round_dir / 'integration-seventh.log').read_text().rstrip().endswith('\nPASS'), 'integration must pass before deploying'
assert not preview.exists(), 'preserve existing preview rounds'
for port in [30467, 18087, 19087]:
    with socket.socket() as s:
        assert s.connect_ex(('127.0.0.1' if port != 30467 else '192.168.0.63', port)) != 0, 'port occupied'
preview.mkdir(mode=0o755)
for name in ['private', 'tls', 'web', 'logs']:
    (preview / name).mkdir(mode=0o700)
for name in ['api.dsn', 'tenant-id', 'browser-identity.json']:
    shutil.copyfile(old / 'private' / name, preview / 'private' / name)
    (preview / 'private' / name).chmod(0o600)
for name in ['web.crt', 'web.key']:
    shutil.copyfile(old / 'tls' / name, preview / 'tls' / name)
    (preview / 'tls' / name).chmod(0o600)
shutil.copyfile(old / 'resolved-profile.yaml', preview / 'resolved-profile.yaml')
shutil.copyfile(round_dir / 'platform-api-gpt-resume-r13-seventh', preview / 'platform-api')
(preview / 'platform-api').chmod(0o700)
runner = (old / 'run-api.py').read_text().replace("bao=b.parent/'gpt-r6-openbao'", "bao=b.parent.parent/'gpt-r6-openbao'").replace('30466', '30467').replace('18086', '18087').replace('19086', '19087')
(preview / 'run-api.py').write_text(runner)
(preview / 'nginx.conf').write_text((old / 'nginx.conf').read_text().replace('30466', '30467').replace('18086', '18087'))
with tarfile.open(round_dir / 'gpt-resume-r13-web-final.tgz') as archive:
    archive.extractall(preview / 'web', filter='data')
(preview / 'web').chmod(0o755)
(preview / 'web/runtime-config.json').write_text(json.dumps({'auth': {'mode': 'native'}}))
image = subprocess.check_output(['docker', 'inspect', 'ops-ui-v12-gpt-r6-web', '--format', '{{.Image}}'], text=True).strip()
subprocess.run(['systemd-run', '--unit=ops-ui-v12-gpt-r13-api', '--property=Restart=on-failure', '/usr/bin/python3', str(preview / 'run-api.py')], check=True)
subprocess.run(['docker', 'run', '-d', '--name', 'ops-ui-v12-gpt-r13-web', '--label', 'ops.local/task=ui-v12-mfa-gpt-20261022', '--network=host', '--read-only', '--tmpfs', '/tmp', '--tmpfs', '/var/cache/nginx', '--tmpfs', '/var/run', '--mount', 'type=bind,src=' + str(preview) + ',dst=/preview,readonly', '--entrypoint', 'nginx', image, '-c', '/preview/nginx.conf', '-g', 'daemon off;'], check=True)
print('new task preview: https://192.168.0.63:30467/login; existing previews preserved')
