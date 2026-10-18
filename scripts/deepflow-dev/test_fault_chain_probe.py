import importlib.util
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("fault_chain_probe", Path(__file__).with_name("fault-chain-probe.py"))
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)


def row(point, code, observations, protocol=20, kind=2):
    return {"tap_side": point, "l7_protocol": protocol, "type": kind, "response_code": code, "observations": observations}


class ReconcileTest(unittest.TestCase):
    def test_only_completed_http_sessions_are_counted(self):
        rows = [
            row("s-p", 200, 950),
            row("s-p", 503, 50),
            row("c", None, 999, protocol=120, kind=2),
            row("c", 200, 7, kind=1),
            row("s", 302, 3),
            {"tap_side": "unknown", "l7_protocol": 20, "type": 2, "response_code": 200, "observations": 4},
        ]
        self.assertEqual(
            probe.point_totals(rows),
            {"s-p": {"observations": 1000, "errors": 50}, "s": {"observations": 3, "errors": 0}},
        )

    def test_exact_population_is_required_at_one_point(self):
        totals = {"s": {"observations": 1000, "errors": 50}, "s-p": {"observations": 1000, "errors": 50}, "c": {"observations": 960, "errors": 50}}
        self.assertEqual(probe.reconciling_points(totals, 1000, 50), ["s", "s-p"])
        self.assertEqual(probe.reconciling_points(totals, 960, 50), ["c"])
        self.assertEqual(probe.reconciling_points(totals, 1000, 49), [])
        self.assertEqual(probe.reconciling_points(totals, 1040, 50), [])

    def test_failure_classes_separate_collector_gap_from_query_gap(self):
        self.assertEqual(
            probe.classify_failure([], {}, 0, False),
            "collector_blackout_downstream_of_agent_or_ingest",
        )
        self.assertEqual(probe.classify_failure([], {}, 442, False), "namespace_without_pair")
        self.assertEqual(probe.classify_failure([], {}, None, False), "no_rows_for_pair_with_unverified_namespace")
        self.assertEqual(
            probe.classify_failure([row("c", None, 90, protocol=120)], {}, 442, False),
            "rows_without_completed_http_sessions",
        )
        self.assertEqual(
            probe.classify_failure([row("s", 200, 960)], {"s": {"observations": 960, "errors": 0}}, 442, True),
            "count_mismatch_after_agent_restart",
        )
        self.assertEqual(
            probe.classify_failure([row("s", 200, 960)], {"s": {"observations": 960, "errors": 0}}, 442, False),
            "count_mismatch",
        )
        # Rows existed but the re-read disagreed: the window was still moving,
        # which is not the same as the pair never being observed.
        self.assertEqual(
            probe.classify_failure([], {"s": {"observations": 1000, "errors": 50}}, 442, False),
            "count_unstable_late_rows",
        )


if __name__ == "__main__":
    unittest.main()
