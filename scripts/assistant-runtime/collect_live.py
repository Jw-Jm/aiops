#!/usr/bin/env python3
"""Collect actual Kubernetes/cAdvisor/log observations for an owned namespace.
No invented samples, backdated values, kubeconfig export or model access.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import re
import subprocess
import time
import urllib.request
import uuid

LABEL = re.compile(r'([a-zA-Z_][a-zA-Z0-9_]*)="((?:[^"\\]|\\.)*)"')
METRIC = re.compile(r'^(container_cpu_usage_seconds_total|container_memory_working_set_bytes)\{(.*)\}\s+([0-9.eE+-]+)(?:\s+([0-9]+))?$')


def kube(*args):
    return subprocess.check_output(['kubectl', '--request-timeout=10s', *args], text=True, stderr=subprocess.PIPE)


def cadvisor(text, pods):
    found = []
    for line in text.splitlines():
        match = METRIC.match(line)
        if not match:
            continue
        labels = {key: json.loads('"' + value + '"') for key, value in LABEL.findall(match[2])}
        pod = pods.get((labels.get('namespace'), labels.get('pod')))
        if pod is None or not labels.get('container') or labels['container'] == 'POD':
            continue
        if labels['container'] not in {c['name'] for c in pod['spec']['containers']}:
            continue
        if match[1] == 'container_cpu_usage_seconds_total' and labels.get('cpu') not in (None, 'total'):
            continue
        found.append((match[1], pod['metadata']['uid'], labels['namespace'], labels['container'], float(match[3]), int(match[4]) if match[4] else None))
    return found


def send(endpoint, path, body, content_type):
    # Fixed loopback task-owned sinks only; refuse redirect and remote endpoints.
    if endpoint not in ('http://127.0.0.1:18428', 'http://127.0.0.1:19428'):
        raise ValueError('owned Victoria sink required')
    request = urllib.request.Request(endpoint + path, data=body.encode(), headers={'Content-Type': content_type}, method='POST')
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, req, fp, code, msg, headers, newurl):
            raise ValueError('sink redirects prohibited')
    with urllib.request.build_opener(NoRedirect).open(request, timeout=10) as response:
        if response.status not in (200, 204):
            raise ValueError('sink refused observations')


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--namespace', required=True)
    p.add_argument('--output', type=Path, required=True)
    p.add_argument('--seconds', type=int, default=420)
    p.add_argument('--interval', type=int, default=5)
    p.add_argument('--identity-from', type=Path, help='reuse owned tenant/source identifiers, recheck actual cluster and namespace')
    args = p.parse_args()
    if not re.fullmatch(r'ops-assistant-live-[0-9]{8}', args.namespace) or not args.output.is_absolute() or args.seconds < 60 or args.seconds > 900 or args.interval < 5:
        p.error('bounded owned namespace/output/window required')
    args.output.mkdir(parents=True, mode=0o700, exist_ok=False)
    tenant = str(uuid.uuid4())
    cluster = json.loads(kube('get', 'namespace', 'kube-system', '-o', 'json'))['metadata']['uid']
    config = {'schemaVersion': 'assistant-live-source/v1', 'tenantId': tenant, 'clusterUid': cluster, 'namespace': args.namespace, 'sourceId': str(uuid.uuid4()), 'metricsSourceId': str(uuid.uuid4()), 'logsSourceId': str(uuid.uuid4()), 'collectorVersion': 'kubernetes-cadvisor-logs/v1', 'collectorDigest': 'sha256:' + hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), 'kubernetesVersion': json.loads(kube('version', '-o', 'json'))['serverVersion']['gitVersion']}
    if args.identity_from:
        prior = json.loads(args.identity_from.read_text())
        if prior['clusterUid'] != cluster or prior['namespace'] != args.namespace:
            raise ValueError('cannot reuse identifiers for a different source scope')
        for key in ('tenantId', 'sourceId', 'metricsSourceId', 'logsSourceId'):
            config[key] = str(uuid.UUID(prior[key]))
        tenant = config['tenantId']
    (args.output / 'config.json').write_text(json.dumps(config, indent=2) + '\n')
    seen = set()
    receipt = {'schemaVersion': 'assistant-live-collection/v1', 'fixtureOnly': False, 'scope': 'owned live canary namespace on actual Kubernetes; not all production namespaces', 'config': config, 'startedAt': datetime.now(timezone.utc).isoformat(), 'cycles': 0, 'metrics': 0, 'logs': 0, 'errors': [], 'identityChangesDiscarded': 0}
    raw = (args.output / 'observations.jsonl').open('x')
    start = time.monotonic()
    while time.monotonic() - start < args.seconds:
        before = time.monotonic()
        try:
            items = json.loads(kube('get', 'pods', '-n', args.namespace, '-l', 'ops.task=assistant-live-20261013', '-o', 'json'))['items']
            pods = {(args.namespace, pod['metadata']['name']): pod for pod in items}
            observed = datetime.now(timezone.utc)
            samples = []
            for node in sorted({p['spec'].get('nodeName') for p in items} - {None}):
                samples.extend(cadvisor(kube('get', '--raw', '/api/v1/nodes/' + node + '/proxy/metrics/cadvisor'), pods))
            # Recheck immutable identity after the cAdvisor read; a name is not UID.
            current = json.loads(kube('get', 'pods', '-n', args.namespace, '-l', 'ops.task=assistant-live-20261013', '-o', 'json'))['items']
            identities = {p['metadata']['uid'] for p in current}
            text = []
            for metric, uid, namespace, container, value, timestamp in samples:
                if uid not in identities:
                    receipt['identityChangesDiscarded'] += 1
                    continue
                labels = {'tenant': tenant, 'cluster': cluster, 'namespace': namespace, 'uid': uid, 'container': container}
                timestamp = timestamp or int(observed.timestamp() * 1000)
                text.append(metric + '{' + ','.join(k + '=' + json.dumps(v) for k, v in labels.items()) + '} ' + str(value) + ' ' + str(timestamp))
                raw.write(json.dumps({'type': 'cadvisor', 'metric': metric, 'labels': labels, 'value': value, 'timestampMs': timestamp}) + '\n')
            for pod in current:
                uid = pod['metadata']['uid']
                labels = {'tenant': tenant, 'cluster': cluster, 'namespace': args.namespace, 'uid': uid}
                timestamp = int(observed.timestamp() * 1000)
                for status in pod['status'].get('containerStatuses', []):
                    fields = dict(labels, container=status['name'])
                    text.append('kube_pod_container_status_restarts_total{' + ','.join(k + '=' + json.dumps(v) for k, v in fields.items()) + '} ' + str(status['restartCount']) + ' ' + str(timestamp))
                    raw.write(json.dumps({'type': 'kubernetes', 'metric': 'kube_pod_container_status_restarts_total', 'labels': fields, 'value': status['restartCount'], 'timestampMs': timestamp}) + '\n')
                    if not status.get('state', {}).get('running'):
                        continue
                    output = kube('logs', '-n', args.namespace, pod['metadata']['name'], '-c', status['name'], '--timestamps', '--tail=200')
                    checked = json.loads(kube('get', 'pod', '-n', args.namespace, pod['metadata']['name'], '-o', 'json'))
                    after_status = next((s for s in checked['status'].get('containerStatuses', []) if s['name'] == status['name']), None)
                    if checked['metadata']['uid'] != uid or after_status is None or after_status['restartCount'] != status['restartCount']:
                        receipt['identityChangesDiscarded'] += 1
                        continue
                    entries = []
                    for line in output.splitlines():
                        stamp, separator, message = line.partition(' ')
                        key = (uid, status['name'], status['restartCount'], stamp, message)
                        if not separator or key in seen:
                            continue
                        seen.add(key)
                        row = dict(labels, _time=stamp, _msg=message, level='info')
                        entries.append(json.dumps(row))
                        raw.write(json.dumps({'type': 'log', 'row': row}) + '\n')
                    if entries:
                        send('http://127.0.0.1:19428', '/insert/jsonline?_stream_fields=tenant,cluster,namespace,uid', '\n'.join(entries) + '\n', 'application/stream+json')
                        receipt['logs'] += len(entries)
                for phase in ('Pending', 'Running', 'Succeeded', 'Failed', 'Unknown'):
                    fields = dict(labels, phase=phase)
                    value = int(pod['status'].get('phase') == phase)
                    text.append('kube_pod_status_phase{' + ','.join(k + '=' + json.dumps(v) for k, v in fields.items()) + '} ' + str(value) + ' ' + str(timestamp))
                    raw.write(json.dumps({'type': 'kubernetes', 'metric': 'kube_pod_status_phase', 'labels': fields, 'value': value, 'timestampMs': timestamp}) + '\n')
            if text:
                send('http://127.0.0.1:18428', '/api/v1/import/prometheus', '\n'.join(text) + '\n', 'text/plain')
                receipt['metrics'] += len(text)
            receipt['cycles'] += 1
        except Exception as error:
            # Never serialize command stderr or kubeconfig/HTTP authorization.
            receipt['errors'].append({'cycle': receipt['cycles'], 'type': type(error).__name__})
        raw.flush()
        receipt['lastObservedAt'] = datetime.now(timezone.utc).isoformat()
        (args.output / 'receipt.json').write_text(json.dumps(receipt, indent=2) + '\n')
        time.sleep(max(0, args.interval - (time.monotonic() - before)))
    raw.close()
    receipt['observationsDigest'] = 'sha256:' + hashlib.sha256((args.output / 'observations.jsonl').read_bytes()).hexdigest()
    receipt['finishedAt'] = datetime.now(timezone.utc).isoformat()
    (args.output / 'receipt.json').write_text(json.dumps(receipt, indent=2) + '\n')
    print(json.dumps({k: receipt[k] for k in ('cycles', 'metrics', 'logs', 'errors')}))
    return int(bool(receipt['errors']) or receipt['metrics'] == 0 or receipt['logs'] == 0)


if __name__ == '__main__':
    raise SystemExit(main())
