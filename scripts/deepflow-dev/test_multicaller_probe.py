import copy
import importlib.util
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("multicaller", Path(__file__).with_name("fault-chain-multicaller-probe.py"))
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)


class OracleTest(unittest.TestCase):
    def test_source_cannot_pass_when_independent_counts_or_window_differ(self):
        measured = [{"responses": 1000, "errors": 50}, {"responses": 1000, "errors": 0}]
        golden = [dict(m, transportFailures=0, finishedAt=9) for m in measured]
        self.assertTrue(probe.measurements_match(measured, golden, [50, 0], 10))
        for key, value in (("responses", 999), ("errors", 1), ("transportFailures", 1), ("finishedAt", 10)):
            changed = copy.deepcopy(golden)
            changed[1][key] = value
            self.assertFalse(probe.measurements_match(measured, changed, [50, 0], 10), key)
        self.assertFalse(probe.measurements_match(measured[:1], golden, [50, 0], 10))
        measured[1]["errors"] = golden[1]["errors"] = 50
        self.assertTrue(probe.measurements_match(measured, golden, [50, 50], 10))

    def test_restarted_container_invalidates_identity(self):
        pod = {"metadata": {"uid": "pod"}, "spec": {"nodeName": "node"}, "status": {"containerStatuses": [{"name": "app", "containerID": "one", "state": {"running": {"startedAt": "t1"}}}]}}
        replacement = copy.deepcopy(pod)
        replacement["status"]["containerStatuses"][0]["containerID"] = "two"
        self.assertNotEqual(probe.native_identity([pod]), probe.native_identity([replacement]))


if __name__ == "__main__":
    unittest.main()
