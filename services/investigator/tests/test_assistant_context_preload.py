import json
from types import SimpleNamespace
from unittest.mock import Mock, patch
import pytest
from investigator.holmes_adapter import investigate


def setup_runtime(steps=None, succeeds=True):
    steps = list(steps or [])
    api = Mock()
    api.request.side_effect = lambda path: list(steps)
    api.allocate.return_value = {"stepId": "context-step", "argsDigest": "fixed"}
    bridge = Mock()
    bridge.catalog.return_value = [SimpleNamespace(name="get_assistant_context", description="Read frozen context", inputSchema={"type": "object"})]
    def call(name, args, allocation):
        result = {"state": "succeeded" if succeeds else "failed", "data": {"requestedAt": "2026-10-16T01:00:00Z"}}
        steps.append({"toolName": name, "state": result["state"], "result": result})
        return result
    bridge.call.side_effect = call
    model = Mock()
    model.provider.return_value = SimpleNamespace()
    job = {"schemaVersion": "investigation-job/v3", "assistant": {"taskKind": "resource_troubleshooting", "promptVersion": "assistant/v1", "resultSchemaVersion": "assistant-answer/v1"}}
    return job, api, bridge, model, steps


def test_context_is_committed_before_first_model_call_even_with_unrelated_prior_step():
    prior = [{"toolName": "resource_history", "state": "succeeded", "result": {"data": {}}}]
    job, api, bridge, model, steps = setup_runtime(prior)
    with patch("investigator.holmes_adapter.ToolCallingLLM") as agent:
        def reply(**kwargs):
            assert any(s["toolName"] == "get_assistant_context" and s["state"] == "succeeded" for s in steps)
            assert "requestedAt" in json.dumps(kwargs["messages"])
            return SimpleNamespace(result="{}")
        agent.return_value.call.side_effect = reply
        investigate(job, model, api, bridge)
    bridge.call.assert_called_once_with("get_assistant_context", {}, api.allocate.return_value)
    assert model.provider.return_value.needs_initial_tools is False


def test_committed_context_is_reused_without_duplicate_tool_call():
    prior = [{"toolName": "get_assistant_context", "state": "succeeded", "result": {"data": {"requestedAt": "frozen"}}}]
    job, api, bridge, model, _ = setup_runtime(prior)
    with patch("investigator.holmes_adapter.ToolCallingLLM") as agent:
        agent.return_value.call.return_value = SimpleNamespace(result="{}")
        investigate(job, model, api, bridge)
    bridge.call.assert_not_called()
    api.allocate.assert_not_called()


def test_failed_context_stops_before_model_and_keeps_failed_step():
    job, api, bridge, model, steps = setup_runtime(succeeds=False)
    with patch("investigator.holmes_adapter.ToolCallingLLM") as agent:
        with pytest.raises(RuntimeError, match="ASSISTANT_CONTEXT_UNAVAILABLE"):
            investigate(job, model, api, bridge)
        agent.assert_not_called()
    model.provider.assert_not_called()
    assert steps[-1]["state"] == "failed"


def test_missing_context_tool_fails_before_model_admission():
    job, api, bridge, model, _ = setup_runtime()
    bridge.catalog.return_value = []
    with pytest.raises(RuntimeError, match="INITIAL_CONTEXT_TOOL_UNAVAILABLE"):
        investigate(job, model, api, bridge)
    api.allocate.assert_not_called()
    model.provider.assert_not_called()
