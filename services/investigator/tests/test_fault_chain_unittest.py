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


class FaultChainFixedCollection(unittest.TestCase):
    def test_missing_network_data_preserves_causal_gaps_without_model(self):
        ledger = Ledger()
        answer = collect({"budget": {"toolCalls": 20, "rawQueries": 0}}, ledger, Bridge())
        self.assertEqual(answer["answerKind"], "diagnosis")
        self.assertEqual(answer["faultChain"], [])
        self.assertIn("committed_scoped_resource_relations", answer["missingEvidence"])
        self.assertEqual([name for name, _ in ledger.allocations], ["get_assistant_context", "get_dependencies", "evaluate_fault_chain"])
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
