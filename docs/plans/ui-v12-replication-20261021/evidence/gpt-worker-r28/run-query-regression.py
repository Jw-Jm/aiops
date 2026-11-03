"""Run only the native Dispatcher regression on the existing isolated task PG."""
from pathlib import Path
import datetime, hashlib, json, os, socket, subprocess
from urllib.parse import quote

root = Path('/data/ops-v12-native-visual-r24-20261026')
run = root / 'r28-worker/query-regression-r1'
assert socket.gethostname() == 'mssc'
assert root.stat().st_uid == 0
assert run.stat().st_uid == 0
password = (root / 'private/pg-password').read_text().strip()
assert password
# This is the task-owned PostgreSQL container, not the shared developer DB.
info = json.loads(subprocess.check_output(['docker', 'inspect', 'ops-v12-native-r24-pg']))[0]
assert info['Name'] == '/ops-v12-native-r24-pg'
assert any(m.get('Source') == str(root / 'pg-data') for m in info['Mounts'])
ports = info['NetworkSettings']['Ports']['5432/tcp']
assert ports == [{'HostIp': '127.0.0.1', 'HostPort': '15454'}]
env = dict(os.environ, SP03_TEST_DATABASE_URL='postgres://postgres:' + quote(password, safe='') + '@127.0.0.1:15454/postgres?sslmode=disable')
pattern = '^TestNativeAssistantAuditQueryToolWithoutBrowserSession$'
started = datetime.datetime.now(datetime.timezone.utc).isoformat()
with (run / 'dispatcher-inflight.log').open('x') as log:
    result = subprocess.run([str(run / 'integration.test'), '-test.v', '-test.run', pattern, '-test.timeout', '10m'], cwd=root / 'source-final-2/test/integration', env=env, stdout=log, stderr=subprocess.STDOUT, timeout=660)
receipt = {'startedAt': started, 'finishedAt': datetime.datetime.now(datetime.timezone.utc).isoformat(), 'exitCode': result.returncode, 'test': pattern, 'database': 'isolated task PostgreSQL17.11 :15454; test-created unique databases; protected audit records retained', 'binarySHA256': hashlib.sha256((run / 'integration.test').read_bytes()).hexdigest(), 'logSHA256': hashlib.sha256((run / 'dispatcher-inflight.log').read_bytes()).hexdigest(), 'scope': 'production native audit query tool without browser session; real isolated PostgreSQL runtime role; revocation refusal; no model call'}
(run / 'result.json').write_text(json.dumps(receipt, indent=2) + '\n')
print(json.dumps(receipt))
raise SystemExit(result.returncode)
