import importlib.util
import unittest
from pathlib import Path

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
