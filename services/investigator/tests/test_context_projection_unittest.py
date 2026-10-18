import copy
import json
import unittest
from types import SimpleNamespace
from unittest.mock import Mock, patch

from investigator.assistant_binding import model_tool_result, output_schema_for_steps, model_tool_names


class HistoricalContextProjection(unittest.TestCase):
    def test_truncated_context_cannot_start_model_or_fresh_reads(self):
        from investigator.holmes_adapter import investigate
        from investigator.budget import ModelCallError
        job = {"schemaVersion": "investigation-job/v3", "assistant": {"taskKind": "resource_troubleshooting", "promptVersion": "assistant/v1", "resultSchemaVersion": "assistant-answer/v1"}}
        api = Mock()
        api.request.return_value = [{"toolName": "get_assistant_context", "state": "succeeded", "result": {"tool": "get_assistant_context", "state": "partial", "data": {"truncated": True, "reason": "max_result_bytes"}}}]
        bridge = Mock(); bridge.catalog.return_value = []
        model = Mock()
        with patch("investigator.holmes_adapter.ToolCallingLLM") as agent:
            agent.return_value.call.return_value = SimpleNamespace(result="{}")
            with self.assertRaises(ModelCallError) as caught:
                investigate(job, model, api, bridge)
        self.assertEqual(caught.exception.kind, "MODEL_CONTEXT_LIMIT")
        model.provider.assert_not_called()
        bridge.call.assert_not_called()

    def test_profile_accepts_one_million_and_rejects_larger_or_boolean_context(self):
        from pathlib import Path
        from investigator.model_profile import AssistantModelProfile
        raw = json.loads((Path(__file__).resolve().parents[3] / "test/fixtures/assistant/model-profile.fixture.json").read_text())
        profile = AssistantModelProfile.model_validate({**raw, "contextWindow": 1000000})
        self.assertEqual(profile.contextWindow, 1000000)
        for invalid in (1000001, True):
            with self.assertRaises(ValueError):
                AssistantModelProfile.model_validate({**raw, "contextWindow": invalid})

    def test_upstream_context_limit_is_a_terminal_model_diagnostic_in_both_calls(self):
        from investigator.holmes_adapter import investigate, CompactionInsufficientError
        from investigator.budget import ModelCallError
        job = {"schemaVersion": "investigation-job/v3", "assistant": {"taskKind": "resource_troubleshooting", "promptVersion": "assistant/v1", "resultSchemaVersion": "assistant-answer/v1"}}
        api = Mock()
        api.request.return_value = [{"toolName": "get_assistant_context", "state": "succeeded", "result": {"tool": "get_assistant_context", "state": "succeeded", "data": {"message": "只解释", "historicalFaultChainContext": {"status": "partial"}}}}]
        bridge = Mock()
        bridge.catalog.return_value = []
        model = Mock()
        model.provider.return_value = SimpleNamespace()
        for repair in (False, True):
            error = CompactionInsufficientError("context too large", events=[], compaction_usage=None)
            with patch("investigator.holmes_adapter.ToolCallingLLM") as agent:
                agent.return_value.call.side_effect = [SimpleNamespace(result="invalid JSON"), error] if repair else error
                with self.assertRaises(ModelCallError) as caught:
                    investigate(job, model, api, bridge)
                self.assertEqual(caught.exception.kind, "MODEL_CONTEXT_LIMIT")

    def test_explicit_history_explanation_and_read_list_keep_small_tool_view(self):
        names = {"get_assistant_context", "resource_history", "resource_findings", "resource_changes", "execute_analysis_query", "query_deepflow"}
        base = {"historicalFaultChainContext": {"status": "partial"}, "history": [{"role": "assistant"}]}
        self.assertEqual(model_tool_names({**base, "message": "只解释原证据"}, names), set())
        self.assertEqual(model_tool_names({**base, "message": "继续读取 resource_history、resource_findings、resource_changes"}, names), set())
        self.assertEqual(model_tool_names({**base, "message": "继续排查网络"}, names), names)
        self.assertEqual(model_tool_names({**base, "message": "继续调用 query_deepflow"}, names), names)
        self.assertEqual(model_tool_names({"message": "只解释"}, names), names)

    def test_full_fact_associations_and_source_quality_survive_large_context_projection(self):
        query = {"queryResultId": "query", "facts": [{"factId": "fact", "value": 50}], "result": {"exact": False, "warnings": ["coverage_gap"]}}
        ref = {"queryResultId": "query", "factId": "fact"}
        data = {"sourceCapabilities": [{"status": "unavailable"}], "historicalFaultChainQueryResults": [query], "historicalFaultChainContext": {"observations": [{"factRefs": [ref], "missingEvidence": ["causal_mechanism_unverified"]}]}, "history": [{"role": "assistant", "content": {"factRefs": [ref]}}]}
        original = copy.deepcopy(data)
        out = model_tool_result("get_assistant_context", {"data": data})["data"]
        self.assertEqual(data, original)
        self.assertEqual(out, original)

    def test_network_relation_step_is_in_provider_reference_manifest(self):
        step = {"toolName": "query_deepflow", "state": "succeeded", "result": {"data": {"nodes": [{"canonicalId": "pod-a"}, {"canonicalId": "pod-b"}], "relationReferences": [{"relationDigest": "sha256:" + "1" * 64, "from": "pod-a", "to": "pod-b", "kind": "communicates_with"}]}}}
        props = output_schema_for_steps([step], "resource_troubleshooting")["properties"]
        self.assertEqual(props["entityRefs"]["items"]["enum"], ["pod-a", "pod-b"])
        self.assertNotEqual(props["faultChain"].get("maxItems"), 0)
