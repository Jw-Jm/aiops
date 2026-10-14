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

    def test_l7_scope_failure_uses_distinct_admitted_l4_fallback(self):
        class DegradedL7Bridge(Bridge):
            def call(self, name, args, allocation):
                result = self.result(name)
                result["budget"]["remaining"] = {"toolCalls": 15, "rawQueries": 5}
                if name == "get_assistant_context":
                    result["data"]["requestedAt"] = "2026-10-14T01:01:00Z"
                    result["data"]["networkCapabilities"] = [{"l7": "live"}]
                elif name == "query_deepflow" and args["operation"] == "get l7 context":
                    # Legacy degradation is committed as succeeded by the
                    # ledger; identical arguments would replay this result.
                    result["errorCode"] = "SOURCE_DEGRADED"
                    result["data"] = {"sourceErrorCode": "SOURCE_SCOPE_UNVERIFIED"}
                return result
        ledger = Ledger()
        collect({"budget": {"toolCalls": 20, "rawQueries": 6}}, ledger, DegradedL7Bridge())
        reads = [args for name, args in ledger.allocations if name == "query_deepflow"]
        self.assertEqual([r["operation"] for r in reads], ["get l7 context", "get network health"])
        self.assertEqual(reads[0]["timeRange"], reads[1]["timeRange"])
        self.assertEqual(reads[0]["resourceCanonicalId"], reads[1]["resourceCanonicalId"])

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
