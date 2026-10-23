"""Guarded real HTTP/SQL boundary tests; raw failure output stays remote-private."""
from pathlib import Path
from urllib.parse import quote
import os,re,subprocess
b=Path('/data/ops-ui-v12-mfa-20261022-r1');r=b/'gpt-admin-r17';os.umask(0o077)
(r/'private').mkdir(mode=0o700,exist_ok=True)
assert b.stat().st_uid==0 and r.stat().st_uid==0
phase='first'
env=dict(os.environ,OPS_UI_V12_NATIVE_IDENTITY_DBTEST='1',OPS_UI_V12_NATIVE_IDENTITY_TASK='ui-v12-p2-01-r1',OPS_UI_V12_NATIVE_IDENTITY_DB_PORT='15453')
owner=(b/'private/owner.password').read_text().strip();root=(b/'private/postgres-bootstrap.password').read_text().strip()
env['OPS_UI_V12_NATIVE_IDENTITY_ADMIN_DSN']='postgres://ui_v12_native_owner:'+quote(owner,safe='')+'@127.0.0.1:15453/ui_v12_native_identity?sslmode=disable'
env['OPS_UI_V12_NATIVE_IDENTITY_BOOTSTRAP_DSN']='postgres://postgres:'+quote(root,safe='')+'@127.0.0.1:15453/ui_v12_native_identity?sslmode=disable'
with (r/'private'/('integration-'+phase+'.raw.log')).open('x') as log:
 p=subprocess.run([str(r/'integration.test'),'-test.run=^TestNativeMFAEnrollmentStepUpAndReset$','-test.v','-test.timeout=20m'],cwd=r/'test/integration',env=env,stdout=log,stderr=subprocess.STDOUT)
raw=(r/'private'/('integration-'+phase+'.raw.log')).read_text()
for value in (owner,root):raw=raw.replace(value,'[REDACTED]')
lines=[]
for line in raw.splitlines():
 if re.search(r'otpauth://|postgres(?:ql)?://|"(?:password|credential|csrfToken|recoveryCodes|otpauthUri|secret|token)"\s*:',line,re.I):
  lines.append('[REDACTED line containing credential material]')
 else:lines.append(line)
(r/('integration-'+phase+'.log')).write_text('\n'.join(lines)+'\n')
print('guarded integration exit status:',p.returncode)
raise SystemExit(p.returncode)
