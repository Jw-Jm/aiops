#!/usr/bin/env python3
"""Run isolated acceptance on 192.168.0.63; credentials stay outside Git."""
import argparse,gzip,json,os,re,subprocess,urllib.parse
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--tests',required=True);p.add_argument('--name',required=True);p.add_argument('--source',default='live-source-20261013-r3');a=p.parse_args()
if not a.name.replace('-', '').isalnum(): raise ValueError('safe unique run name required')
base=Path('/opt/ops-assistant-dev-20261012')
values=dict(line.split('=',1) for line in (base/'database.env').read_text().splitlines() if '=' in line)
bao=dict(line.split('=',1) for line in (base/'bao/token.env').read_text().splitlines() if '=' in line)
env=os.environ.copy();env.update(SP03_TEST_DATABASE_URL='postgres://postgres:'+urllib.parse.quote(values['POSTGRES_PASSWORD'],safe='')+'@127.0.0.1:15439/postgres?sslmode=disable',SP03_TEST_OPENBAO_URL='https://127.0.0.1:18200',SP03_TEST_OPENBAO_CA_FILE=str(base/'bao/vault-ca.pem'),SP03_TEST_OPENBAO_TOKEN=bao['BAO_DEV_ROOT_TOKEN_ID'],OPS_ASSISTANT_REAL_MODEL='1',OPS_ASSISTANT_RUNTIME_ACCEPTANCE='1',OPS_ASSISTANT_LIVE_SOURCE_DIR=str(base/a.source),OPS_ASSISTANT_LIVE_REPORT=str(base/(a.name+'-sources.json')),OPS_ASSISTANT_RUNTIME_PRIVATE=str(base/'runtime-private'),OPS_ASSISTANT_TEST_INVESTIGATOR_IMAGE='ops.local/amd64-poc/investigator:20261010-r12',OPS_ASSISTANT_TEST_PYTHON_SOURCE=str(base/'python/src'))
env.setdefault('OPS_ASSISTANT_DEEPFLOW_REPORT', str(base/(a.name+'-deepflow.json')))
env.setdefault('OPS_ASSISTANT_DEEPFLOW_MCP_REPORT', str(base/(a.name+'-mcp.json')))
listing=subprocess.run([str(base/'runtime-stage.test'),'-test.list',a.tests],capture_output=True,text=True,check=True)
if not any(line.startswith('Test') for line in listing.stdout.splitlines()): raise ValueError('no integration test matches the requested pattern')
with (base/(a.name+'.log')).open('x') as log:
 r=subprocess.run([str(base/'runtime-stage.test'),'-test.run',a.tests,'-test.v','-test.timeout','24m'],cwd=base/'repo/test/integration',env=env,stdout=log,stderr=subprocess.STDOUT)
result_text = (base/(a.name+'.log')).read_text()
if r.returncode == 0 and (not re.search(r'^--- PASS: Test', result_text, re.MULTILINE) or re.search(r'^--- SKIP: Test', result_text, re.MULTILINE)): raise ValueError('skipped/empty runs are not acceptance')
print('runtime exit',r.returncode)
raise SystemExit(r.returncode)
