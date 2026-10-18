#!/usr/bin/env python3
"""Recompute six retention checks; provider acceptance is not platform acceptance."""
import argparse
import json
import math
from datetime import datetime
from pathlib import Path
from urllib.parse import urlsplit


def unix(value):
    return int(datetime.fromisoformat(value.replace('Z', '+00:00')).timestamp())


def select_observations(rows, context, config, limit):
    windows = [(unix(context['request'][role]['from']), unix(context['request'][role]['to'])) for role in ('baseline', 'fault', 'recovery')]
    resources = {context['request']['targetCanonicalId']}
    for edge in context['edges']:
        resources.update((edge['relation']['from'], edge['relation']['to']))
    uids = {urlsplit(resource).path.split('/')[-1] for resource in resources}
    selected = []
    for row in rows:
        labels = row.get('row', {}) if row.get('type') == 'log' else row.get('labels', {})
        if any(labels.get(key) != config[value] for key, value in (('tenant', 'tenantId'), ('cluster', 'clusterUid'), ('namespace', 'namespace'))) or labels.get('uid') not in uids:
            continue
        if 'timestampMs' in row:
            stamp = row['timestampMs'] / 1000
        elif labels.get('_time'):
            stamp = datetime.fromisoformat(labels['_time'].replace('Z', '+00:00')).timestamp()
        else:
            continue
        if any(start <= stamp < end for start, end in windows):
            selected.append(row)
            if len(selected) >= limit:
                break
    return selected


def comparison_matches(answer, rows, require_delta):
    values = {row['dimension']: row['value'] for row in rows}
    expected = {key: values[key] for key in ('current', 'baseline')}
    if require_delta:
        if len([key for key in ('delta', 'increase', 'decrease', 'unchanged') if key in answer]) != 1:
            return False
        expected['delta'] = values['current'] - values['baseline']
    for key, value in expected.items():
        actual = answer.get(key)
        if key == 'delta' and key not in answer:
            directions = [name for name in ('increase', 'decrease', 'unchanged') if name in answer]
            if len(directions) != 1:
                return False
            direction = directions[0]
            actual = answer[direction]
            if isinstance(actual, bool) or not isinstance(actual, (int, float)) or actual < 0:
                return False
            actual *= -1 if direction == 'decrease' else 1
            if direction == 'unchanged' and actual != 0:
                return False
        if isinstance(actual, bool) or not isinstance(actual, (int, float)) or not math.isfinite(actual) or actual != value:
            return False
    return True


def verify(context, results, receipt, response):
    request = context['request']
    windows = {role: {'fromUnix': unix(request[role]['from']), 'toUnix': unix(request[role]['to'])}
               for role in ('baseline', 'fault', 'recovery')}
    answer = json.loads(response['answer'])

    def query(dataset, role):
        matches = [r for r in results if r['datasetId'] == dataset and r['timeRange']['from'] == request[role]['from'] and r['timeRange']['to'] == request[role]['to']]
        # The retained SQL export used ->> for exact, yielding JSON text "true".
        if len(matches) != 1 or matches[0]['status'] != 'complete' or not (matches[0]['exact'] is True or matches[0]['exact'] == 'true'):
            raise ValueError('unique complete committed query required')
        return matches[0]['rows']

    checks = {
        'windows': answer.get('windows') == windows,
        'httpFault': comparison_matches(answer.get('httpFault', {}), query('network.http-summary', 'fault'), False),
        'tcpFault': comparison_matches(answer.get('tcpFault', {}), query('network.tcp-summary', 'fault'), True),
        'httpRecovery': comparison_matches(answer.get('httpRecovery', {}), query('network.http-summary', 'recovery'), True),
        'missingEvidence': answer.get('missingEvidence') == [gap for edge in context['edges'] for gap in edge['missingEvidence']],
        'sourceRole': answer.get('sourceRole') == {'committedContext': '已提交证据', 'rawObservationIndex': '原始观测'},
    }
    covered = (datetime.fromisoformat(receipt['startedAt'].replace('Z', '+00:00')).timestamp() <= windows['baseline']['fromUnix']
               and datetime.fromisoformat(receipt['lastObservedAt'].replace('Z', '+00:00')).timestamp() >= windows['recovery']['toUnix'])
    target = urlsplit(request['targetCanonicalId'])
    same_tenant = receipt['config']['tenantId'] == target.netloc
    same_cluster = receipt['config']['clusterUid'] == target.path.split('/')[1]
    usage = response['usage']
    # Anthropic-compatible usage separates fresh input from cache reads/writes.
    tokens = sum(usage.get(key, 0) for key in ('input_tokens', 'cache_read_input_tokens', 'cache_creation_input_tokens'))
    completed = response['stopReason'] == 'end_turn'
    elapsed = response.get('elapsedSeconds')
    within_deadline = type(elapsed) in (int, float) and 0 <= elapsed <= 120
    return {'schemaVersion': 'long-context-reassessment/v1', 'checks': checks, 'checksPassed': sum(checks.values()),
            'collectionCoversCommittedWindows': covered, 'sameTenant': same_tenant, 'sameCluster': same_cluster,
            'inputTokensIncludingCache': tokens, 'outputTokens': usage['output_tokens'],
            'normalCompletion': completed, 'withinProbeDeadline': within_deadline,
            'passed': all(checks.values()) and covered and same_tenant and same_cluster and completed and within_deadline,
            'platformRuntimeVerified': False, 'humanQualityReview': 'pending'}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('runtime-report', 'query-results', 'collection-receipt', 'response', 'output'):
        parser.add_argument('--' + name, type=Path, required=True)
    args = parser.parse_args()
    result = verify(json.loads(args.runtime_report.read_text())['context'], json.loads(args.query_results.read_text()),
                    json.loads(args.collection_receipt.read_text()), json.loads(args.response.read_text()))
    with args.output.open('x') as out:
        out.write(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(result, ensure_ascii=False))
    return 0 if result['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
