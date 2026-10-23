"""Run only against the existing, explicitly marked task-owned PostgreSQL."""
import os
from pathlib import Path
import subprocess
from urllib.parse import quote

task = Path('/data/ops-ui-v12-mfa-20261022-r1')
round_dir = task / 'gpt-resume-r13'
env = dict(os.environ)
env.update(
    OPS_UI_V12_NATIVE_IDENTITY_DBTEST='1',
    OPS_UI_V12_NATIVE_IDENTITY_TASK='ui-v12-p2-01-r1',
    OPS_UI_V12_NATIVE_IDENTITY_DB_PORT='15453',
    OPS_UI_V12_NATIVE_IDENTITY_ADMIN_DSN='postgres://ui_v12_native_owner:'
    + quote((task / 'private/owner.password').read_text().strip(), safe='')
    + '@127.0.0.1:15453/ui_v12_native_identity?sslmode=disable',
    OPS_UI_V12_NATIVE_IDENTITY_BOOTSTRAP_DSN='postgres://postgres:'
    + quote((task / 'private/postgres-bootstrap.password').read_text().strip(), safe='')
    + '@127.0.0.1:15453/ui_v12_native_identity?sslmode=disable',
)
with (round_dir / 'http-password-error-final.log').open('x') as log:
    result = subprocess.run(
        [str(round_dir / 'gpt-resume-r13-password-final.test'), '-test.run=^TestNativeHTTPSessionFlow$',
         '-test.v', '-test.timeout=15m'],
        cwd=round_dir / 'test/integration', env=env, stdout=log, stderr=subprocess.STDOUT,
    )
print('integration exit status:', result.returncode)
raise SystemExit(result.returncode)
