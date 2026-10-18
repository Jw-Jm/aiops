import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('verify_long_context', Path(__file__).with_name('verify_long_context.py'))
verification = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verification)


class LongContextVerificationTests(unittest.TestCase):
    def test_raw_rows_must_match_tenant_uid_and_half_open_windows(self):
        start = '2026-10-18T01:00:00Z'
        end = '2026-10-18T01:01:00Z'
        context = {'request': {'targetCanonicalId': 'k8s+v1://tenant/cluster/core/Pod/uid', **{role: {'from': start, 'to': end} for role in ('baseline', 'fault', 'recovery')}}, 'edges': []}
        config = {'tenantId': 'tenant', 'clusterUid': 'cluster', 'namespace': 'owned'}
        labels = {'tenant': 'tenant', 'cluster': 'cluster', 'namespace': 'owned', 'uid': 'uid'}
        row = {'type': 'kubernetes', 'labels': labels, 'timestampMs': verification.unix(start)*1000}
        rows = [{**row, 'timestampMs': row['timestampMs']-1}, {**row, 'timestampMs': verification.unix(end)*1000},
                {**row, 'labels': {**labels, 'tenant': 'other'}}, {**row, 'labels': {**labels, 'uid': 'recreated'}}, row]
        self.assertEqual(verification.select_observations(rows, context, config, 700), [row])

    def test_recovery_direction_is_required(self):
        rows = [{'dimension': 'current', 'value': 0}, {'dimension': 'baseline', 'value': 5}, {'dimension': 'decrease', 'value': 5}]
        for change in ({'increase': 5}, {'delta': 5}, {}, {'decrease': True}, {'decrease': -5}, {'delta': -5, 'increase': 5}):
            self.assertFalse(verification.comparison_matches({'current': 0, 'baseline': 5, **change}, rows, True))
        for change in ({'decrease': 5}, {'delta': -5}):
            self.assertTrue(verification.comparison_matches({'current': 0, 'baseline': 5, **change}, rows, True))

    def test_wrong_collection_window_fails_even_when_six_answers_match(self):
        windows = {'baseline': {'from': '2026-10-18T01:00:00Z', 'to': '2026-10-18T01:01:00Z'},
                   'fault': {'from': '2026-10-18T01:01:00Z', 'to': '2026-10-18T01:02:00Z'},
                   'recovery': {'from': '2026-10-18T01:02:00Z', 'to': '2026-10-18T01:03:00Z'}}
        context = {'request': {**windows, 'targetCanonicalId': 'k8s+v1://tenant/cluster/core/Pod/uid'}, 'edges': [{'missingEvidence': ['gap']}]}
        results = []
        for dataset, role, current, baseline in [('network.http-summary', 'fault', 5, 1), ('network.tcp-summary', 'fault', 100, 0), ('network.http-summary', 'recovery', 0, 5)]:
            results.append({'datasetId': dataset, 'timeRange': windows[role], 'status': 'complete', 'exact': True,
                            'rows': [{'dimension': 'current', 'value': current}, {'dimension': 'baseline', 'value': baseline}]})
        import json
        response = {'answer': json.dumps({'windows': {k: {'fromUnix': verification.unix(w['from']), 'toUnix': verification.unix(w['to'])} for k, w in windows.items()},
            'httpFault': {'current': 5, 'baseline': 1}, 'tcpFault': {'current': 100, 'baseline': 0, 'increase': 100},
            'httpRecovery': {'current': 0, 'baseline': 5, 'decrease': 5}, 'missingEvidence': ['gap'],
            'sourceRole': {'committedContext': '已提交证据', 'rawObservationIndex': '原始观测'}}),
            'usage': {'input_tokens': 410, 'cache_read_input_tokens': 119552, 'output_tokens': 2895}, 'stopReason': 'end_turn', 'elapsedSeconds': 31.5}
        receipt = {'config': {'tenantId': 'tenant', 'clusterUid': 'cluster'}, 'startedAt': windows['baseline']['from'], 'lastObservedAt': windows['recovery']['to']}
        result = verification.verify(context, results, receipt, response)
        self.assertTrue(result['passed'])
        self.assertEqual(result['inputTokensIncludingCache'], 119962)
        response['elapsedSeconds'] = 121
        self.assertFalse(verification.verify(context, results, receipt, response)['passed'])
        response['elapsedSeconds'] = 31.5
        receipt['startedAt'] = '2026-10-18T02:50:00Z'
        result = verification.verify(context, results, receipt, response)
        self.assertEqual(result['checksPassed'], 6)
        self.assertFalse(result['passed'])
        self.assertFalse(result['collectionCoversCommittedWindows'])

    def test_boolean_and_nonfinite_counts_do_not_equal_numbers(self):
        rows = [{'dimension': 'current', 'value': 0}, {'dimension': 'baseline', 'value': 1}]
        for value in (False, float('nan'), float('inf'), '0'):
            self.assertFalse(verification.comparison_matches({'current': value, 'baseline': 1}, rows, False))


if __name__ == '__main__':
    unittest.main()
