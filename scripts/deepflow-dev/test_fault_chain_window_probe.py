import importlib.util
import unittest
import json
import tempfile
import urllib.error
from pathlib import Path
from unittest.mock import patch

spec = importlib.util.spec_from_file_location(
    "fault_chain_window_probe", Path(__file__).with_name("fault-chain-window-probe.py")
)
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)


def golden(codes, refused):
    return {"fromUnix": 1, "toUnix": 61, "codes": codes, "refused": refused, "transportFailures": 0, "uniqueClientPorts": 1000 + refused}


def measurement(responses, errors, failures, records):
    return {"responses": responses, "errors": errors, "failureObservations": failures, "tcpRecords": records}


GOLDEN = [golden({"503": 10, "200": 990}, 0), golden({"503": 50, "200": 950}, 100), golden({"200": 1000}, 0)]
MEASURED = [measurement(1000, 10, 0, 1000), measurement(1000, 50, 100, 1100), measurement(1000, 0, 0, 1000)]


class WindowReconcileTest(unittest.TestCase):
    def test_initial_backend_failure_preserves_golden_and_failure_report(self):
        pods = [{"metadata": {"name": name, "uid": name+"-uid", "labels": {"app": name}},
                 "status": {"phase": "Running", "podIP": "10.0.0.2", "containerStatuses": [{"containerID": "containerd://test"}]}}
                for name in ("fault-chain-client", "fault-chain-server")]
        with tempfile.TemporaryDirectory() as directory:
            report = Path(directory)/"windows.json"
            with patch('sys.argv', ['probe', '--report', str(report), '--wait-seconds', '30']), \
                 patch.object(probe, 'kubectl', side_effect=[json.dumps({'items': pods}), json.dumps(GOLDEN)]), \
                 patch.object(probe, 'query', side_effect=urllib.error.URLError('connection refused')), \
                 patch.object(probe.time, 'sleep'), patch.object(probe.time, 'time', side_effect=[0, 1, 2, 3, 31, 32]), patch('builtins.print'):
                self.assertEqual(probe.main(), 1)
            result = json.loads(report.read_text())
            self.assertEqual(result['status'], 'FAIL')
            self.assertFalse(result['stable'])
            self.assertEqual(result['sourceErrors'], [{'poll': 1, 'type': 'URLError'}])
            self.assertEqual(json.loads(report.with_suffix('.golden.json').read_text())['golden'], GOLDEN)

    def test_three_windows_reconcile_against_their_own_record(self):
        self.assertTrue(probe.reconcile(MEASURED, GOLDEN))

    def test_each_criterion_is_required(self):
        weak = [
            ("responses", 999),
            ("errors", 51),
            ("failureObservations", 99),
            ("tcpRecords", 1099),
        ]
        for key, value in weak:
            changed = [dict(m) for m in MEASURED]
            changed[1][key] = value
            self.assertFalse(probe.reconcile(changed, GOLDEN), key)
        self.assertFalse(probe.reconcile(MEASURED[:2], GOLDEN), "missing window")
        self.assertFalse(probe.reconcile(MEASURED + [MEASURED[0]], GOLDEN), "extra window")
        self.assertFalse(probe.reconcile([], GOLDEN), "no windows")

    def test_golden_transport_failure_never_passes(self):
        broken = [dict(g) for g in GOLDEN]
        broken[2]["transportFailures"] = 1
        self.assertFalse(probe.reconcile(MEASURED, broken))

    def test_reused_client_ports_cannot_prove_one_record_per_attempt(self):
        broken = [dict(g) for g in GOLDEN]
        broken[0]["uniqueClientPorts"] = 962
        self.assertFalse(probe.reconcile(MEASURED, broken))


if __name__ == "__main__":
    unittest.main()
