"""Keep the failed first container; expose only Web/TLS/config to non-root nginx."""
from pathlib import Path
import json
import os
import subprocess

p = Path('/data/ops-ui-v12-mfa-20261022-r1/gpt-resume-r13/preview')
old = json.loads(subprocess.check_output(['docker', 'inspect', 'ops-ui-v12-gpt-r13-web']))[0]
assert old['State']['Status'] == 'exited' and old['State']['ExitCode'] == 1
assert any(m['Source'] == str(p) for m in old['Mounts'])
os.chown(p / 'tls', 0, 101)
(p / 'tls').chmod(0o750)
for name in ['web.crt', 'web.key']:
    os.chown(p / 'tls' / name, 0, 101)
    (p / 'tls' / name).chmod(0o640)
args = ['docker', 'run', '-d', '--name', 'ops-ui-v12-gpt-r13-web-r2', '--label', 'ops.local/task=ui-v12-mfa-gpt-20261022', '--user', '101:101', '--network=host', '--read-only', '--tmpfs', '/tmp', '--tmpfs', '/var/cache/nginx', '--tmpfs', '/var/run']
for source, dest in [('web', '/preview/web'), ('tls', '/preview/tls'), ('nginx.conf', '/etc/nginx/nginx.conf')]:
    args += ['--mount', 'type=bind,src=' + str(p / source) + ',dst=' + dest + ',readonly']
args += ['--entrypoint', 'nginx', old['Image'], '-e', '/dev/stderr', '-c', '/etc/nginx/nginx.conf', '-g', 'daemon off;']
subprocess.run(args, check=True)
