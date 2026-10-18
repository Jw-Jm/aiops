#!/usr/bin/env python3
"""One real page run, with the owned CA and explicit explanation/follow-up."""
import json
import os
import shutil
import subprocess
import time
import urllib.parse
from pathlib import Path

base = Path('/opt/ops-five-source-reverify-20261018/round5')
old = Path('/opt/ops-assistant-dev-20261012')
private = old/'runtime-private'
ca = old/'operational-browser-root-ca.pem'
assert ca.is_file()
with (base/'qualification-page.log').open('x') as log:
    subprocess.run(['python3',str(base/'fault-chain-probe.py'),'--report',str(base/'qualification-page.json'),
                    '--count','300','--wait-seconds','90'],stdout=log,stderr=subprocess.STDOUT,timeout=150,check=True)
receipt = private/'browser-receipt.json'
address = private/'api-address.json'
for path in (receipt, address):
    if path.exists():
        shutil.move(str(path), str(base/('previous-'+path.name)))
values = dict(line.split('=', 1) for line in (old/'database.env').read_text().splitlines() if '=' in line)
bao = dict(line.split('=', 1) for line in (old/'bao/token.env').read_text().splitlines() if '=' in line)
env = os.environ.copy()
env.update({
    'SP03_TEST_DATABASE_URL':'postgres://postgres:'+urllib.parse.quote(values['POSTGRES_PASSWORD'],safe='')+'@127.0.0.1:15439/postgres?sslmode=disable',
    'SP03_TEST_OPENBAO_URL':'https://127.0.0.1:18200',
    'SP03_TEST_OPENBAO_CA_FILE':str(old/'bao/vault-ca.pem'),
    'SP03_TEST_OPENBAO_TOKEN':bao['BAO_DEV_ROOT_TOKEN_ID'],
    'OPS_TEST_ARCHIVE_HOST_NETWORK':'1',
    'OPS_TEST_ARCHIVE_IMAGE':subprocess.check_output(['docker','image','inspect','--format','{{.Id}}','ops.local/amd64-poc/seaweedfs:4.47'],text=True).strip(),
    'OPS_ASSISTANT_REAL_MODEL':'1', 'OPS_ASSISTANT_RUNTIME_ACCEPTANCE':'1',
    'OPS_ASSISTANT_DEEPFLOW_GRAPH_ONLY':'1', 'OPS_ASSISTANT_OPERATIONAL_SOURCES':'1',
    'OPS_ASSISTANT_LIVE_SOURCE_DIR':str(base/'live-source'), 'OPS_ASSISTANT_RUNTIME_PRIVATE':str(private),
    'OPS_ASSISTANT_TEST_INVESTIGATOR_IMAGE':'ops.local/amd64-poc/investigator:20261010-r12',
    'OPS_ASSISTANT_TEST_PYTHON_SOURCE':str(base.parent/'repo/services/investigator/src'),
    'OPS_FAULT_CHAIN_RUNTIME':'1', 'OPS_FAULT_CHAIN_MODEL':'1',
    'OPS_FAULT_CHAIN_PROOF':str(base/'fault-window.json'), 'OPS_FAULT_CHAIN_WINDOWS_PROOF':str(base/'windows.json'),
    'OPS_ASSISTANT_DEEPSEEK_KEY_FILE':str(private/'deepseek-acceptance.key'),
    'OPS_FAULT_CHAIN_RUNTIME_REPORT':str(base/'page-ledger.json'),
})
go_log = (base/'page-go.log').open('x')
test = subprocess.Popen([str(base/'ops-five-source-20261018-next.test'),'-test.run','^TestAssistantActualOIDCAndBrowserRuntime$','-test.v','-test.timeout','20m'],
    cwd=base.parent/'repo/test/integration',env=env,stdout=go_log,stderr=subprocess.STDOUT)
try:
    deadline = time.monotonic()+240
    while test.poll() is None and time.monotonic()<deadline:
        if address.exists():
            try:
                published = json.loads(address.read_text())
                if published.get('address'):
                    break
            except json.JSONDecodeError:
                pass
        time.sleep(1)
    else:
        raise RuntimeError('API_ADDRESS_NOT_PUBLISHED')
    browser_env = os.environ.copy()
    browser_env.pop('NODE_TLS_REJECT_UNAUTHORIZED',None)
    browser_env.update({
        'NODE_EXTRA_CA_CERTS':str(ca), 'OPS_FAULT_CHAIN_RUNTIME':'1', 'OPS_FAULT_CHAIN_MODEL':'1',
        'OPS_WEB_QA_CDP':'http://127.0.0.1:19228', 'OPS_ASSISTANT_RUNTIME_PRIVATE':str(private),
        'OPS_FAULT_CHAIN_PROOF':str(base/'fault-window.json'),
        'OPS_WEB_QA_OUTPUT':str(base/'page-results'), 'OPS_WEB_QA_REPORT':str(base/'page-playwright.json'),
    })
    with (base/'page-browser.log').open('x') as browser_log:
        browser = subprocess.run(['/opt/ops-assistant-browser-20261014/node',str(base/'e2e/node_modules/@playwright/test/cli.js'),
            'test','--config','playwright.config.mjs'],cwd=base/'e2e',env=browser_env,stdout=browser_log,stderr=subprocess.STDOUT,timeout=420)
    if browser.returncode != 0:
        receipt.write_text(json.dumps({'passed':False,'runtimeId':published['runtimeId'],'profileReference':published['profileReference'],'modelQualityAcceptance':False}))
    code = test.wait(timeout=90)
    result = json.loads(receipt.read_text()) if receipt.exists() else {}
    assert browser.returncode == 0 and code == 0 and result.get('passed') is True
    assert len(result.get('modelTurns',[])) == 2, 'explanation and follow-up must both run'
    assert all(turn['state'] in ('completed','partial') and not turn.get('errorCode') for turn in result['modelTurns'])
    (base/'page-receipt.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
    print(json.dumps({'passed':True,'modelTurns':len(result['modelTurns']),'tlsVerification':True}))
except Exception as error:
    (base/'page-failure.json').write_text(json.dumps({'passed':False,'type':type(error).__name__,'stage':str(error) if isinstance(error,RuntimeError) else 'page'})+'\n')
    raise
finally:
    if test.poll() is None:
        test.terminate()
        try:
            test.wait(timeout=20)
        except subprocess.TimeoutExpired:
            test.kill()
            test.wait(timeout=10)
    go_log.close()
