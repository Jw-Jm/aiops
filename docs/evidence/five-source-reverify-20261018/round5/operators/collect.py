#!/usr/bin/env python3
"""One bounded fresh collection after fixing the owned client's timed exit."""
import hashlib
import json
import socket
import subprocess
import time
from datetime import datetime
from pathlib import Path

BASE = Path('/opt/ops-five-source-reverify-20261018/round5')
OLD = BASE.parent
NS = 'ops-assistant-deepflow-20261013'
RELEASE = 'fault-chain-20261014'


def kube(*args):
    return subprocess.check_output(['kubectl', '--request-timeout=15s', '-n', NS, *args], text=True, timeout=150)


def main():
    for port in (21416, 21417):
        with socket.create_connection(('127.0.0.1', port), timeout=5):
            pass
    deployment = json.loads(kube('get', 'deployment', 'fault-chain-client', '-o', 'json'))
    assert deployment['metadata']['labels']['ops.platform.io/test-release'] == RELEASE
    assert deployment['spec']['replicas'] == 1
    assert deployment['spec']['template']['spec']['containers'][0]['name'] == 'app'
    patch = [
        {'op': 'replace', 'path': '/spec/template/spec/containers/0/command',
         'value': ['python3', '-u', '-c', 'import signal; signal.pause()']},
        {'op': 'replace', 'path': '/spec/strategy/rollingUpdate',
         'value': {'maxSurge': 0, 'maxUnavailable': 1}},
    ]
    if deployment['spec']['template']['spec']['containers'][0]['command'] != patch[0]['value'] or deployment['spec']['strategy']['rollingUpdate'] != patch[1]['value']:
        print(kube('patch', 'deployment', 'fault-chain-client', '--type=json', '-p', json.dumps(patch)), flush=True)
    print(kube('rollout', 'status', 'deployment/fault-chain-client', '--timeout=120s'), flush=True)
    pods = json.loads(kube('get', 'pods', '-l', 'ops.platform.io/test-release=' + RELEASE, '-o', 'json'))
    (BASE/'native-pods-after-rollout.json').write_text(json.dumps([
        {'name': p['metadata']['name'], 'uid': p['metadata']['uid'],
         'containers': [{'id': c['containerID'], 'restartCount': c['restartCount'],
                         'startedAt': c.get('state', {}).get('running', {}).get('startedAt')}
                        for c in p['status'].get('containerStatuses', [])]}
        for p in pods['items'] if p['metadata']['labels'].get('app') in ('fault-chain-client', 'fault-chain-server')
    ], indent=2)+'\n')
    with (BASE/'collection.log').open('x') as log:
        collector = subprocess.Popen(['python3', str(BASE/'collect_live.py'), '--canary-set', 'fault-chain',
            '--namespace', NS, '--output', str(BASE/'live-source'), '--seconds', '660', '--interval', '5',
            '--identity-from', str(OLD/'live-source-r4/config.json')], stdout=log, stderr=subprocess.STDOUT)
        try:
            print('collector started; waiting for five-minute metric lookback', flush=True)
            time.sleep(340)
            assert collector.poll() is None, 'collector exited before frozen windows'
            with (BASE/'windows.log').open('x') as probe_log:
                subprocess.run(['python3', str(BASE/'fault-chain-window-probe.py'), '--report', str(BASE/'windows.json'),
                    '--wait-seconds', '180'], stdout=probe_log, stderr=subprocess.STDOUT, timeout=450, check=True)
            print('three-window probe completed', flush=True)
            assert collector.wait(timeout=360) == 0, 'collector failed'
        finally:
            if collector.poll() is None:
                collector.terminate()
                collector.wait(timeout=20)
    project()


def project():
    raw = (BASE/'windows.json').read_bytes()
    w = json.loads(raw)
    receipt = json.loads((BASE/'live-source/receipt.json').read_text())
    cfg = json.loads((BASE/'live-source/config.json').read_text())
    assert w['status'] == 'PASS' and w['stable']
    assert not receipt['errors'] and receipt['identityChangesDiscarded'] == 0
    assert datetime.fromisoformat(receipt['startedAt']).timestamp() + 300 <= w['golden'][0]['fromUnix']
    assert datetime.fromisoformat(receipt['lastObservedAt']).timestamp() >= w['golden'][2]['toUnix']
    g = w['golden'][1]
    proof = {'schemaVersion':'fault-chain-http-proof/v1', 'status':'PASS', 'namespace':w['namespace'],
        'release':w['release'], 'nativePods':w['nativePods'], 'window':{k:g[k] for k in ('fromUnix','toUnix')},
        'golden':{'responses':sum(g['codes'].values()), 'errors':g['codes'].get('503',0), 'transportFailures':g['transportFailures']},
        'observationPoint':w['observationPoints']['http'], 'timeReliable':False, 'stable':True, 'confirmMatches':True,
        'sourceProofFile':'windows.json', 'sourceProofDigest':'sha256:'+hashlib.sha256(raw).hexdigest(),
        'tenantId':cfg['tenantId'], 'scope':'fault window projection of the same reconciled three-window oracle'}
    (BASE/'fault-window.json').write_text(json.dumps(proof,indent=2)+'\n')
    print(json.dumps({'status':'PASS','cycles':receipt['cycles'],'metrics':receipt['metrics'],'sourceErrors':w['sourceErrors'],'window':proof['window']}), flush=True)


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        (BASE/'collection-failure.json').write_text(json.dumps({'status':'FAIL','type':type(error).__name__})+'\n')
        raise
