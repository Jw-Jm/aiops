#!/usr/bin/env python3
"""Run the context regression against the existing disposable remote database."""
import json
import os
import subprocess
import urllib.parse
from pathlib import Path

base = Path('/opt/ops-five-source-reverify-20261018/round5')
values = dict(line.split('=', 1) for line in Path('/opt/ops-assistant-dev-20261012/database.env').read_text().splitlines() if '=' in line)
env = os.environ.copy()
env['SP03_TEST_DATABASE_URL'] = 'postgres://postgres:' + urllib.parse.quote(values['POSTGRES_PASSWORD'], safe='') + '@127.0.0.1:15439/postgres?sslmode=disable'
with (base/'context-regression.log').open('x') as log:
    code = subprocess.run([str(base/'ops-five-source-20261018-next.test'), '-test.run', '^TestAssistantContextSurvivesCapabilityTimeout$', '-test.v', '-test.timeout', '5m'],
        cwd=base.parent/'repo/test/integration', env=env, stdout=log, stderr=subprocess.STDOUT, timeout=330).returncode
text = (base/'context-regression.log').read_text()
result = {'exit':code, 'passed':code == 0 and '--- PASS: TestAssistantContextSurvivesCapabilityTimeout' in text, 'skipped':'--- SKIP:' in text}
(base/'context-regression.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps(result))
raise SystemExit(0 if result['passed'] and not result['skipped'] else 1)
