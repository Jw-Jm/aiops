import unittest
from investigator.fault_chain import collect


class Ledger:
    def __init__(self, committed=False):
        self.allocations = []
        self.committed = committed
        self.saved = {}

    def allocate(self, name, args):
        allocation = {"stepId": str(len(self.allocations) + 1), "argsDigest": "fixed", "committed": self.committed}
        self.allocations.append((name, args))
        if self.committed:
            self.saved[allocation["stepId"]] = Bridge.result(name)
        return allocation

    def request(self, path):
        if path == "/budget": return {"toolCalls": 10, "rawQueries": 0}
        return self.saved[path.split("/")[2]]


class Bridge:
    @staticmethod
    def result(name):
        if name == "get_assistant_context":
            data = {"faultChainRequest": {"schemaVersion": "fault-chain-request/v1", "targetCanonicalId": "target", "fault": {"from": "2026-10-14T01:00:00Z", "to": "2026-10-14T01:01:00Z", "timezone": "UTC"}, "maxEdges": 4}, "sourceCapabilities": []}
        elif name == "evaluate_fault_chain":
            data = {"context": {"edges": [], "queryResultRefs": [], "evidenceRefs": [], "missingEvidence": ["committed_scoped_resource_relations"], "asOf": "2026-10-14T01:01:00Z"}, "contentDigest": "sha256:" + "0" * 64}
        else:
            data = {"relationReferences": []}
        return {"state": "succeeded", "data": data, "budget": {"remaining": {"toolCalls": 10, "rawQueries": 0}}}

    def call(self, name, args, allocation):
        return self.result(name)


class BudgetBridge(Bridge):
    def __init__(self, raw_queries, peers=2, baseline=True):
        self.remaining = {"toolCalls": 30, "rawQueries": raw_queries}
        self.peers = peers
        self.baseline = baseline
        self.queries = []

    def call(self, name, args, allocation):
        self.remaining["toolCalls"] -= 1
        result = self.result(name)
        if name == "get_assistant_context":
            data = result["data"]
            data["requestedAt"] = "2026-10-14T01:02:00Z"
            if self.baseline:
                data["faultChainRequest"]["baseline"] = {"from": "2026-10-14T00:58:00Z", "to": "2026-10-14T00:59:00Z", "timezone": "UTC"}
            data["faultChainRequest"]["recovery"] = {"from": "2026-10-14T01:01:00Z", "to": "2026-10-14T01:02:00Z", "timezone": "UTC"}
            data["sourceCapabilities"] = [{"datasetId": d, "operation": op, "status": "qualification_configured"} for d in ("network.http-summary", "network.tcp-summary") for op in ("aggregate", "compare")]
        elif name == "query_deepflow":
            self.remaining["rawQueries"] -= 1
        elif name == "get_dependencies":
            result["data"] = {"nodes": [{"canonicalId": f"peer-{n}", "kind": "Pod"} for n in range(self.peers)], "relationReferences": [{"from": "target", "to": f"peer-{n}", "kind": "communicates_with", "relationDigest": f"digest-{n}"} for n in reversed(range(self.peers))]}
        elif name == "execute_analysis_query":
            intent = args["intent"]
            self.remaining["rawQueries"] -= 2 if intent["operation"] == "compare" else 1
            self.queries.append(intent)
            result["data"] = {"storedQueryResult": {"queryResultId": f"q{len(self.queries)}", "facts": []}}
        elif name == "evaluate_fault_chain":
            result["data"]["context"]["queryResultRefs"] = [f"q{n+1}" for n in range(len(self.queries))]
        if min(self.remaining.values()) < 0:
            raise AssertionError("collector overspent admitted budget")
        result["budget"]["remaining"] = dict(self.remaining)
        return result


class FaultChainFixedCollection(unittest.TestCase):
    def test_fault_http_reaches_each_peer_before_transport_or_recovery(self):
        bridge = BudgetBridge(5)
        answer = collect({"budget": {"toolCalls": 30, "rawQueries": 5}}, Ledger(), bridge)
        self.assertEqual([q["networkSelection"]["peerCanonicalId"] for q in bridge.queries], ["peer-0", "peer-1"])
        self.assertEqual([q["datasetId"] for q in bridge.queries], ["network.http-summary", "network.http-summary"])
        self.assertTrue(all(q["timeRange"]["from"] == "2026-10-14T01:00:00Z" for q in bridge.queries))
        self.assertEqual(bridge.remaining["rawQueries"], 0)
        self.assertIn("recovery_remaining_checks_not_completed", answer["missingEvidence"])
        self.assertEqual(answer["queryResultRefs"], ["q1", "q2"])

    def test_primary_network_observation_precedes_optional_related_history(self):
        class OperationalBridge(BudgetBridge):
            def call(self, name, args, allocation):
                result = super().call(name, args, allocation)
                if name == "get_assistant_context":
                    result["data"]["faultChainRequest"]["schemaVersion"] = "fault-chain-request/v2"
                return result
        ledger = Ledger()
        bridge = OperationalBridge(10, peers=4)
        bridge.remaining["toolCalls"] = 18
        collect({"budget": dict(bridge.remaining)}, ledger, bridge)
        reads = ledger.allocations
        network = [i for i, (name, _) in enumerate(reads) if name == "execute_analysis_query"]
        related = [i for i, (name, args) in enumerate(reads) if name == "resource_history" and args["canonicalId"] != "target"]
        self.assertTrue(network, "optional history exhausted the primary observation budget")
        self.assertTrue(all(i > network[0] for i in related))

    def test_non_network_targets_keep_optional_related_history(self):
        for kind in ("Node", "PersistentVolumeClaim"):
            class OperationalBridge(BudgetBridge):
                def call(self, name, args, allocation):
                    result = super().call(name, args, allocation)
                    if name == "get_assistant_context":
                        result["data"]["faultChainRequest"]["schemaVersion"] = "fault-chain-request/v2"
                        result["data"]["binding"] = {"resourceContext": {"resources": [{"canonicalId": "target", "kind": kind}]}}
                    return result
            ledger = Ledger()
            bridge = OperationalBridge(10, peers=1)
            collect({"budget": dict(bridge.remaining)}, ledger, bridge)
            self.assertIn(("resource_history", {"canonicalId": "peer-0", "windowRole": "fault"}), ledger.allocations)
            self.assertFalse(bridge.queries)

    def test_bidirectional_populations_are_stable_and_bounded(self):
        for reverse in (False, True):
            for max_edges in (1, 2):
                class BidirectionalBridge(BudgetBridge):
                    def call(self, name, args, allocation):
                        result = super().call(name, args, allocation)
                        if name == "get_assistant_context":
                            result["data"]["faultChainRequest"]["maxEdges"] = max_edges
                        elif name == "get_dependencies":
                            relations = result["data"]["relationReferences"]
                            relations.append({**relations[0], "from": "peer-0", "to": "target", "relationDigest": "aaa" if reverse else "zzz"})
                        return result
                bridge = BidirectionalBridge(20, peers=1, baseline=False)
                answer = collect({"budget": {"toolCalls": 30, "rawQueries": 20}}, Ledger(), bridge)
                primary = [q["networkSelection"]["direction"] for q in bridge.queries if q["datasetId"] == "network.http-summary" and q["timeRange"]["from"] == "2026-10-14T01:00:00Z"]
                self.assertEqual(primary, ["outgoing", "incoming"][:max_edges])
                self.assertEqual("remaining_relations_not_checked" in answer["missingEvidence"], max_edges == 1)

    def test_recovery_compares_fault_even_without_baseline(self):
        bridge = BudgetBridge(6, peers=1, baseline=False)
        collect({"budget": {"toolCalls": 30, "rawQueries": 6}}, Ledger(), bridge)
        recovery = [q for q in bridge.queries if q["timeRange"]["from"] == "2026-10-14T01:01:00Z"]
        self.assertEqual(len(recovery), 1)
        self.assertEqual(recovery[0]["operation"], "compare")
        self.assertEqual(recovery[0]["comparisonTimeRange"]["from"], "2026-10-14T01:00:00Z")

    def test_shared_resource_parents_remain_entity_references(self):
        class SharedBridge(Bridge):
            def call(self, name, args, allocation):
                result = self.result(name)
                if name == "get_dependencies":
                    result["data"] = {"nodes": [{"canonicalId": "peer", "kind": "Pod"}], "relationReferences": [{"from": "target", "to": "peer", "kind": "communicates_with", "relationDigest": "fixed"}]}
                elif name == "evaluate_fault_chain":
                    result["data"]["context"]["edges"] = [{"relation": {"from": "target", "to": "peer", "kind": "communicates_with"}, "relationDigest": "fixed", "observations": [{"supports": [], "conflicts": [], "resourceHealth": {"canonicalId": "shared-node"}, "relatedRelations": [{"from": "other-caller", "to": "shared-node"}]}], "missingEvidence": ["causal_mechanism_unverified"], "nextChecks": []}]
                return result
        ledger = Ledger()
        answer = collect({"budget": {"toolCalls": 20, "rawQueries": 0}}, ledger, SharedBridge())
        self.assertIn(("get_resource_health", {"canonicalId": "peer"}), ledger.allocations)
        self.assertEqual(answer["entityRefs"], ["other-caller", "peer", "shared-node", "target"])

    def test_relation_read_uses_frozen_fault_window_and_plans_historical_peer(self):
        class RelationBridge(BudgetBridge):
            def call(self, name, args, allocation):
                result = super().call(name, args, allocation)
                if name == "query_deepflow":
                    # The live overlay has expired; only this admitted projection
                    # knows the peer that communicated during the frozen window.
                    result["data"] = {"nodes": [{"canonicalId": "peer-0", "kind": "Pod"}], "relationReferences": [{"from": "target", "to": "peer-0", "kind": "communicates_with", "relationDigest": "frozen"}]}
                elif name == "get_dependencies":
                    result["data"] = {"nodes": [], "relationReferences": []}
                return result
        ledger = Ledger()
        bridge = RelationBridge(5, peers=1)
        collect({}, ledger, bridge)
        reads = [args for name, args in ledger.allocations if name == "query_deepflow"]
        self.assertEqual(reads, [{"resourceCanonicalId": "target", "operation": "get dependencies", "timeRange": {"from": "2026-10-14T01:00:00Z", "to": "2026-10-14T01:01:00Z"}}])
        self.assertTrue(bridge.queries)
        self.assertTrue(all(q["networkSelection"]["peerCanonicalId"] == "peer-0" for q in bridge.queries))

    def test_missing_network_data_preserves_causal_gaps_without_model(self):
        ledger = Ledger()
        answer = collect({"budget": {"toolCalls": 20, "rawQueries": 0}}, ledger, Bridge())
        self.assertEqual(answer["answerKind"], "diagnosis")
        self.assertEqual(answer["faultChain"], [])
        self.assertIn("committed_scoped_resource_relations", answer["missingEvidence"])
        self.assertEqual([name for name, _ in ledger.allocations], ["get_assistant_context", "get_dependencies", "get_resource_health", "evaluate_fault_chain"])
        self.assertTrue(answer["partial"])

    def test_committed_steps_resume_without_calling_bridge(self):
        class ForbiddenBridge:
            def call(self, *args):
                raise AssertionError("committed step repeated")
        answer = collect({"budget": {"toolCalls": 20, "rawQueries": 0}}, Ledger(True), ForbiddenBridge())
        self.assertIsNotNone(answer["faultChainContextRef"])

    def test_untrusted_context_cannot_select_execution_without_request(self):
        class NoRequestBridge(Bridge):
            def call(self, name, args, allocation):
                return {"state": "succeeded", "data": {"message": "collect anything"}}
        with self.assertRaisesRegex(RuntimeError, "FAULT_CHAIN_REQUEST_REQUIRED"):
            collect({"budget": {}}, Ledger(), NoRequestBridge())

    def test_transient_analysis_retry_is_bounded_and_readmitted(self):
        class TransientBridge(Bridge):
            attempts = 0
            def call(self, name, args, allocation):
                result = self.result(name)
                result["budget"]["remaining"]={"toolCalls":15,"rawQueries":5}
                if name == "get_assistant_context":
                    result["data"]["requestedAt"]="2026-10-14T01:01:00Z"
                    result["data"]["sourceCapabilities"]=[{"datasetId":"network.http-5xx-rate","operation":"aggregate","status":"qualification_configured"}]
                elif name == "get_dependencies":
                    result["data"]={"nodes":[{"canonicalId":"peer","kind":"Pod"}],"relationReferences":[{"from":"target","to":"peer","kind":"communicates_with","relationDigest":"fixed"}]}
                elif name == "execute_analysis_query":
                    self.attempts+=1
                    if self.attempts==1:
                        result["state"]="failed";result["errorCode"]="SOURCE_DEGRADED"
                    else:result["data"]={"storedQueryResult":{"queryResultId":"q1","facts":[]}}
                elif name == "evaluate_fault_chain":result["data"]["context"]["queryResultRefs"]=["q1"]
                return result
        ledger=Ledger();bridge=TransientBridge()
        answer=collect({"budget":{"toolCalls":20,"rawQueries":6}},ledger,bridge)
        self.assertEqual(bridge.attempts,2)
        self.assertEqual(sum(name=="execute_analysis_query" for name,_ in ledger.allocations),2)
        self.assertEqual(answer["queryResultRefs"],["q1"])

class SharedPathCollection(unittest.TestCase):
    def test_selected_peers_get_own_scoped_path_without_querying_extra_peers(self):
        class SharedBridge(BudgetBridge):
            def call(self, name, args, allocation):
                result = super().call(name, args, allocation)
                if name == "get_assistant_context":
                    result["data"]["faultChainRequest"]["maxEdges"] = 1
                if name == "get_dependencies" and args["canonicalId"] == "target":
                    result["data"]["nodes"].append({"canonicalId": "shared-node", "kind": "Node"})
                return result
        bridge, ledger = SharedBridge(10, peers=2), Ledger()
        answer = collect({"budget": {"toolCalls": 30, "rawQueries": 10}}, ledger, bridge)
        paths = [args for name, args in ledger.allocations if name == "get_dependencies"]
        self.assertEqual(paths, [{"canonicalId": "target", "depth": 1}, {"canonicalId": "peer-0", "depth": 1}])
        self.assertTrue(all(q["networkSelection"]["peerCanonicalId"] == "peer-0" for q in bridge.queries))
        health = [args["canonicalId"] for name, args in ledger.allocations if name == "get_resource_health"]
        self.assertEqual(health, ["shared-node", "target", "peer-0"])
        self.assertIn("remaining_relations_not_checked", answer["missingEvidence"])

class LinkedGapChecks(unittest.TestCase):
    def test_health_and_dependency_checks_execute_fixed_tool_without_network_or_model(self):
        for tool in ("get_resource_health", "get_dependencies"):
            class CheckBridge(Bridge):
                def call(self, name, args, allocation):
                    result = self.result(name)
                    if name == "get_assistant_context":
                        result["data"]["faultChainRequest"]["readOnlyCheck"] = {"tool": tool, "canonicalId": "peer", "gapCode": "original-gap"}
                    elif name == "evaluate_fault_chain" and tool == "get_resource_health":
                        result["data"]["context"]["resourceHealthChecks"] = [{"canonicalId": "peer"}]
                    return result
            ledger = Ledger()
            answer = collect({"budget": {"toolCalls": 20, "rawQueries": 6}}, ledger, CheckBridge())
            self.assertEqual([name for name, _ in ledger.allocations], ["get_assistant_context", tool, "evaluate_fault_chain"])
            self.assertEqual(ledger.allocations[1][1], {"canonicalId": "peer", **({"depth": 1} if tool == "get_dependencies" else {})})
            self.assertIn("original-gap", answer["summary"])
            if tool == "get_resource_health": self.assertIn("peer", answer["entityRefs"])

    def test_statistics_use_original_peer_direction_and_windows_even_if_graph_changed(self):
        class CheckBridge(BudgetBridge):
            def call(self, name, args, allocation):
                result = super().call(name, args, allocation)
                if name == "get_assistant_context":
                    result["data"]["faultChainRequest"]["readOnlyCheck"] = {"tool": "execute_analysis_query", "canonicalId": "old-peer", "direction": "incoming", "gapCode": "network_window_statistics"}
                return result
        ledger, bridge = Ledger(), CheckBridge(8, peers=3)
        answer = collect({"budget": {"toolCalls": 30, "rawQueries": 8}}, ledger, bridge)
        self.assertEqual(len(bridge.queries), 4)
        self.assertTrue(all(q["networkSelection"] == {"targetCanonicalId": "target", "peerCanonicalId": "old-peer", "direction": "incoming"} for q in bridge.queries))
        self.assertFalse(any(name in ("query_deepflow", "get_resource_health") for name, _ in ledger.allocations))
        self.assertEqual(answer["queryResultRefs"], ["q1", "q2", "q3", "q4"])

    def test_failed_health_check_still_evaluates_and_keeps_gap(self):
        class CheckBridge(Bridge):
            def call(self, name, args, allocation):
                result = self.result(name)
                if name == "get_assistant_context":
                    result["data"]["faultChainRequest"]["readOnlyCheck"] = {"tool": "get_resource_health", "canonicalId": "target", "gapCode": "shared_resource_cause_not_checked"}
                elif name == "get_resource_health":
                    result["state"] = "failed"
                return result
        answer = collect({"budget": {"toolCalls": 10, "rawQueries": 0}}, Ledger(), CheckBridge())
        self.assertIn("get_resource_health_not_completed", answer["missingEvidence"])
        self.assertIsNotNone(answer["faultChainContextRef"])

class OperationalCollectorTests(unittest.TestCase):
    def test_three_resource_types_share_collector_and_node_pvc_do_not_query_deepflow(self):
        for kind in ("Pod", "Node", "PersistentVolumeClaim"):
            class OperationalBridge(BudgetBridge):
                def call(self, name, args, allocation):
                    result = super().call(name, args, allocation)
                    if name == "get_assistant_context":
                        result["data"]["faultChainRequest"]["schemaVersion"] = "fault-chain-request/v2"
                        result["data"]["binding"] = {"resourceContext": {"resources": [{"canonicalId": "target", "kind": kind}]}}
                    return result
            ledger = Ledger()
            bridge = OperationalBridge(8, peers=1)
            result = collect({"budget": {"toolCalls": 30, "rawQueries": 8}}, ledger, bridge)
            names = [n for n, _ in ledger.allocations]
            self.assertIn("resource_history", names)
            self.assertIn("resource_findings", names)
            self.assertIn("resource_changes", names)
            self.assertEqual(names.count("evaluate_fault_chain"), 1)
            if kind != "Pod": self.assertNotIn("query_deepflow", names)
            self.assertTrue(result["partial"])

    def test_node_and_pvc_state_each_inapplicable_read_explicitly(self):
        for kind, marker in (("Node", "logs_and_pod_metrics_not_applicable_for_Node"), ("PersistentVolumeClaim", "pvc_metrics_logs_not_applicable")):
            class OperationalBridge(BudgetBridge):
                def call(self, name, args, allocation):
                    result = super().call(name, args, allocation)
                    if name == "get_assistant_context":
                        result["data"]["faultChainRequest"]["schemaVersion"] = "fault-chain-request/v2"
                        result["data"]["binding"] = {"resourceContext": {"resources": [{"canonicalId": "target", "kind": kind}]}}
                    return result
            ledger = Ledger()
            bridge = OperationalBridge(8, peers=1)
            result = collect({"budget": {"toolCalls": 30, "rawQueries": 8}}, ledger, bridge)
            names = [n for n, _ in ledger.allocations]
            failures = result["missingEvidence"]
            # No Pod-scoped dataset or Pod network template is borrowed.
            self.assertNotIn("query_deepflow", names)
            for leaked in ("metrics.pod-restarts", "metrics.pod-memory", "metrics.pod-cpu", "logs.records"):
                self.assertFalse(
                    any(args.get("intent", {}).get("datasetId") == leaked
                        for name, args in ledger.allocations if name == "execute_analysis_query"),
                    (kind, leaked),
                )
            self.assertTrue(any(marker in failure for failure in failures), (kind, failures))
            self.assertIn("network_not_applicable_for_" + kind, failures)
            self.assertIn("resource_history", names)
            self.assertIn("resource_findings", names)
            self.assertIn("resource_changes", names)

    def test_node_keeps_its_admitted_metric_dataset(self):
        class OperationalBridge(BudgetBridge):
            def call(self, name, args, allocation):
                result = super().call(name, args, allocation)
                if name == "get_assistant_context":
                    result["data"]["faultChainRequest"]["schemaVersion"] = "fault-chain-request/v2"
                    result["data"]["binding"] = {"resourceContext": {"resources": [{"canonicalId": "target", "kind": "Node"}]}}
                    result["data"]["sourceCapabilities"] = [
                        {"datasetId": "metrics.node-ready", "operation": "compare", "status": "qualification_configured"}
                    ]
                return result
        ledger = Ledger()
        bridge = OperationalBridge(8, peers=1)
        collect({"budget": {"toolCalls": 30, "rawQueries": 8}}, ledger, bridge)
        intents = [args.get("intent", {}).get("datasetId") for name, args in ledger.allocations if name == "execute_analysis_query"]
        self.assertIn("metrics.node-ready", intents)
        self.assertNotIn("metrics.pod-restarts", intents)

    def test_recovery_uses_live_budget_instead_of_cached_receipt(self):
        class RecoveredLedger(Ledger):
            def __init__(self):
                super().__init__(committed=True)
                self.budget_reads = 0
            def request(self, path):
                if path == "/budget":
                    self.budget_reads += 1
                    return {"toolCalls": 2, "rawQueries": 0}
                return super().request(path)
        ledger = RecoveredLedger()
        collect({"budget": {"toolCalls": 40, "rawQueries": 10}}, ledger, Bridge())
        self.assertGreaterEqual(ledger.budget_reads, 2)
        self.assertNotIn("resource_history", [name for name, _ in ledger.allocations])
