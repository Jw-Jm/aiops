import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('evaluate', Path(__file__).with_name('evaluate.py'))
evaluate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evaluate)
SUITE = Path(__file__).resolve().parents[2] / 'test/fixtures/assistant/evaluation/acceptance-v1.json'


class EvaluationTest(unittest.TestCase):
    def setUp(self):
        self.suite, self.digest = evaluate.load_suite(SUITE)
        self.window = {'from': '2026-10-13T00:00:00Z', 'to': '2026-10-13T00:30:00Z', 'timezone': 'Asia/Shanghai'}
        self.intent = {'datasetId': 'incidents.records', 'operation': 'aggregate', 'metrics': ['incident_count'], 'dimensions': [], 'filters': [], 'sort': [], 'limit': 50, 'timeRange': self.window}
        self.result = {'datasetId': 'incidents.records', 'unit': 'incidents', 'dimension': '', 'status': 'complete', 'exact': True, 'truncated': False, 'timeRange': self.window, 'rows': [{'dimension': '', 'value': 3}]}
        self.stored = {'queryResultId': 'query-1', 'result': self.result, 'facts': [{'factId': 'fact-1', 'row': 0, 'column': 'value', 'value': 3, 'unit': 'incidents', 'exact': True}]}
        self.answer = {'answerKind': 'data', 'queryResultRefs': ['query-1'], 'factRefs': [{'queryResultId': 'query-1', 'factId': 'fact-1'}], 'entityRefs': [], 'evidenceRefs': [], 'missingEvidence': [], 'hypotheses': [], 'summary': '统计结果'}
        self.turn = {'state': 'completed', 'requestedAt': '2026-10-13T00:30:00.123Z', 'requestedTimeRange': self.window, 'expectedNamespace': 'eval-query-r1', 'goldenCount': 3, 'answer': self.answer, 'steps': [{'toolName': 'get_assistant_context', 'state': 'succeeded'}, {'toolName': 'execute_analysis_query', 'state': 'succeeded', 'intent': self.intent, 'result': {'data': {'storedQueryResult': self.stored}}}]}
        self.row = self.observation('zh-001', [self.turn])

    def observation(self, id, turns):
        return {'schemaVersion': 'assistant-evaluation-observation/v1', 'suiteDigest': self.digest, 'suiteVersion': self.suite['version'], 'caseId': id, 'repeat': 1, 'profileReference': 'test-profile', 'runState': 'completed', 'turns': turns}

    def score(self):
        return evaluate.score_case(self.suite['cases'][0], self.row)

    def test_correct_nonzero_query_passes(self):
        metrics, reasons, violations = self.score()
        self.assertEqual(metrics['query'], [True])
        self.assertEqual((reasons, violations), ([], []))

    def test_published_readonly_v2_tools_do_not_admit_execution_tools(self):
        root=Path(__file__).resolve().parents[2]
        for name in ('resource_history','resource_changes','evaluate_fault_chain'):
            self.assertTrue((root/'api/assistant-tools'/f'{name}-v3.schema.json').is_file())
            self.turn['steps'].append({'toolName':name,'state':'failed'})
        self.assertEqual(self.score()[2],[])
        self.turn['steps'].append({'toolName':'execute_command','state':'failed'})
        self.assertIn('non_readonly_tool:execute_command',self.score()[2])

    def test_unsupported_is_failure_for_supported_query(self):
        self.turn['steps'] = self.turn['steps'][:1]
        self.turn['answer'] = {'answerKind': 'unsupported'}
        self.assertEqual(self.score()[0]['query'], [False])

    def test_wrong_number_unit_scope_time_and_order_fail(self):
        for key, value in [('unit', 'milliseconds'), ('rows', [{'dimension': '', 'value': 0}]), ('timeRange', {'from': 'wrong'})]:
            with self.subTest(key=key):
                saved = self.result[key]
                self.result[key] = value
                self.assertEqual(self.score()[0]['query'], [False])
                self.result[key] = saved
        self.turn['requestedAt'] = '2026-10-13T00:31:00Z'
        self.assertEqual(self.score()[0]['intent'], [False])
        self.turn['requestedAt'] = '2026-10-13T00:30:00.123Z'
        self.intent['filters'] = [{'field': 'namespace', 'operator': 'eq', 'value': 'outside'}]
        self.assertEqual(self.score()[0]['intent'], [False])
        self.intent['filters'] = []
        self.intent['sort'] = [{'field': 'incident_count', 'direction': 'asc'}]
        self.assertEqual(self.score()[0]['intent'], [False])

    def test_forged_fact_or_evidence_and_fact_cell_fail(self):
        self.answer['factRefs'][0]['factId'] = 'forged'
        self.answer['evidenceRefs'] = ['uncommitted']
        self.assertIn('illegal_reference', self.score()[2])
        self.stored['facts'][0]['value'] = 0
        self.assertIn('fact_cell_mismatch', self.score()[2])

    def test_changed_observation_cannot_change_frozen_golden(self):
        self.turn['goldenCount'] = 0
        self.result['rows'][0]['value'] = 0
        self.assertEqual(self.score()[0]['query'], [False])

    def test_multiturn_must_preserve_uid_and_window(self):
        case = self.suite['cases'][3]
        answer = {'answerKind': 'diagnosis', 'missingEvidence': ['缺少退出日志'], 'hypotheses': [], 'nextChecks': ['读取退出日志']} 
        turns = [{'state': 'completed', 'answer': answer, 'steps': [], 'boundEntityIds': ['uid-one'], 'bindingTimeRange': {'timeRange': self.window}}, {'state': 'completed', 'answer': answer, 'steps': [], 'boundEntityIds': ['uid-one'], 'referenceResolution': {'status': 'resolved', 'canonicalId': 'uid-other'}, 'bindingTimeRange': {'timeRange': {'from': 'changed'}}}]
        row = self.observation(case['id'], turns)
        row.update(incidentCountBefore=0, incidentCountAfter=1)
        _, reasons, violations = evaluate.score_case(case, row)
        self.assertIn('followup_reference_not_bound_uid', reasons)
        self.assertIn('followup_time_window_changed', reasons)
        self.assertIn('resource_investigation_created_incident', violations)

    def test_missing_cases_and_human_review_never_pass(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'observations.jsonl'
            path.write_text(json.dumps(self.row) + '\n')
            report = evaluate.evaluate(SUITE, path)
            self.assertEqual(report['qualityVerdict'], 'INCOMPLETE')
            self.assertEqual(len(report['missingCases']), 4)
            for case in self.suite['cases'][1:]:
                kind = 'clarification' if case['id'] == 'zh-005' else 'diagnosis'
                answer = {'answerKind': kind, 'clarification': {'question': '请明确目标'}, 'missingEvidence': ['缺少日志'], 'hypotheses': [], 'nextChecks': ['读取退出日志']} 
                turns = [{'state': 'awaiting_user' if kind == 'clarification' else 'completed', 'answer': answer, 'steps': [], 'boundEntityIds': ['uid-one'], 'bindingTimeRange': {'timeRange': self.window}} for _ in case['messages']]
                if len(turns) == 2:
                    turns[1]['referenceResolution'] = {'status': 'resolved', 'canonicalId': 'uid-one'}
                row = self.observation(case['id'], turns)
                row.update(incidentCountBefore=0, incidentCountAfter=0)
                with path.open('a') as out:
                    out.write(json.dumps(row) + '\n')
            report = evaluate.evaluate(SUITE, path)
            self.assertEqual(report['qualityVerdict'], 'REVIEW_PENDING')
            self.assertEqual(report['humanReviewPending'], ['zh-002', 'zh-003', 'zh-004'])

    def test_duplicate_or_wrong_suite_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'observations.jsonl'
            path.write_text((json.dumps(self.row) + '\n') * 2)
            with self.assertRaises(ValueError):
                evaluate.evaluate(SUITE, path)
            self.row['suiteDigest'] = 'changed'
            path.write_text(json.dumps(self.row) + '\n')
            with self.assertRaises(ValueError):
                evaluate.evaluate(SUITE, path)


if __name__ == '__main__':
    unittest.main()
