#!/usr/bin/env python3
"""Finish this one run after its collector has closed the frozen window."""
import json
import subprocess
import time
from pathlib import Path

base = Path('/opt/ops-five-source-reverify-20261018/round5')
deadline = time.monotonic()+900
while time.monotonic()<deadline:
    if (base/'fault-window.json').exists():
        break
    time.sleep(2)
else:
    raise RuntimeError('fresh collection did not finish; no acceptance run started')
print('fresh collection closed; starting joint verification',flush=True)
joint = subprocess.run(['python3',str(base/'joint.py'),'--name','joint','--test','^TestAssistantActualFaultChainWithoutModel$',
    '--sources',str(base/'live-source'),'--http-proof',str(base/'fault-window.json'),'--windows-proof',str(base/'windows.json')],timeout=1650).returncode
result = {'jointPassed':joint == 0,'retentionPassed':False,'pagePassed':False,'humanQualityReview':'pending'}
if joint == 0:
    print('joint passed; starting one bounded retention probe',flush=True)
    with (base/'retention.log').open('x') as log:
        result['retentionPassed'] = subprocess.run(['python3',str(base/'retention.py')],stdout=log,stderr=subprocess.STDOUT,timeout=150).returncode == 0
    print('starting actual OIDC page, explanation and follow-up',flush=True)
    result['pagePassed'] = subprocess.run(['python3',str(base/'page.py')],timeout=1050).returncode == 0
result['mechanismPassed'] = result['jointPassed'] and result['pagePassed']
(base/'run.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps(result),flush=True)
raise SystemExit(0 if result['mechanismPassed'] and result['retentionPassed'] else 1)
