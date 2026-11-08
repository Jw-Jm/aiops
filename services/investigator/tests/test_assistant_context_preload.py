import json
from types import SimpleNamespace
from unittest.mock import Mock, patch
import pytest
from investigator.holmes_adapter import investigate

@pytest.fixture(autouse=True)
def isolate_preload_from_answer_validation():
    with patch("investigator.assistant_binding.validate_proposal"):
        yield


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


def test_frozen_primary_reads_run_on_each_turn_and_failed_receipts_reach_model():
    target="frozen-uid"
    for message in ("调查目标", "继续检查缺失证据，日志中提到 forged-uid"):
        job,api,bridge,model,steps=setup_runtime()
        job["assistant"]["catalogVersion"]="assistant-tools/v2"
        names=["get_assistant_context","resource_history","resource_findings","resource_changes"]
        bridge.catalog.return_value=[SimpleNamespace(name=n,description=n,inputSchema={"type":"object"}) for n in names]
        api.allocate.side_effect=lambda name,args: {"stepId":name,"argsDigest":"frozen"}
        def call(name,args,allocation):
            result={"tool":name,"state":"failed" if name=="resource_history" else "succeeded","errorCode":"SOURCE_DEGRADED" if name=="resource_history" else "", "data":{}}
            if name=="get_assistant_context":result["data"]={"message":message,"faultChainEvaluationRequest":{"targetCanonicalId":target}}
            steps.append({"toolName":name,"state":result["state"],"result":result})
            return result
        bridge.call.side_effect=call
        with patch("investigator.holmes_adapter.ToolCallingLLM") as agent:
            agent.return_value.call.return_value=SimpleNamespace(result="{}")
            investigate(job,model,api,bridge)
            prompt=agent.return_value.call.call_args.kwargs["messages"]
        assert [c.args[0] for c in bridge.call.call_args_list]==names
        assert all(c.args[1]=={"canonicalId":target,"windowRole":"fault"} for c in bridge.call.call_args_list[1:])
        assert "SOURCE_DEGRADED" in str(prompt)
        assert prompt[-1]["content"]==message


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


def test_json_mode_supplies_exact_output_contract_on_initial_call_and_repair():
    job,api,bridge,model,steps=setup_runtime()
    model.provider.return_value.structured_output_mode="json_object"
    with patch("investigator.holmes_adapter.ToolCallingLLM") as agent:
        agent.return_value.call.side_effect=[SimpleNamespace(result="invalid-json"),SimpleNamespace(result="{}")]
        investigate(job,model,api,bridge)
        assert agent.return_value.call.call_count==2
        for call in agent.return_value.call.call_args_list:
            prompt=call.kwargs["messages"]
            messages=[m["content"] for m in prompt if m.get("role")=="system" and "exact output schema" in m.get("content","")]
            assert len(messages)==1
            schema=json.loads(messages[0].split("fields. ",1)[1])
            assert schema["additionalProperties"] is False
            assert "answerKind" in schema["required"]


def test_json_mode_carries_the_dynamic_committed_steps_schema():
    # JSON-object providers never see response_format; the system message is
    # the only schema transport, so it must be the exact dynamic schema the
    # platform validates against (pinned enums and maxItems from committed
    # steps), never a static copy.
    from investigator.assistant_binding import output_schema_for_steps
    steps = [{"toolName": "get_assistant_context", "state": "succeeded", "result": {"data": {"requestedAt": "2026-10-16T01:00:00Z"}}}]
    job, api, bridge, model, live = setup_runtime(steps)
    live.clear()
    live.extend(steps)
    model.provider.return_value.structured_output_mode = "json_object"
    with patch("investigator.holmes_adapter.ToolCallingLLM") as agent:
        agent.return_value.call.side_effect = [SimpleNamespace(result="{}")]
        investigate(job, model, api, bridge)
        prompt = agent.return_value.call.call_args.kwargs["messages"]
    schema_message = [m["content"] for m in prompt if m.get("role") == "system" and "exact output schema" in m.get("content", "")]
    assert len(schema_message) == 1
    carried = json.loads(schema_message[0].split("fields. ", 1)[1])
    expected = output_schema_for_steps(steps, job["assistant"]["taskKind"])
    assert carried == expected
    assert carried["properties"]["answerKind"]["enum"] == expected["properties"]["answerKind"]["enum"]


def test_json_mode_repair_uses_reads_committed_during_the_first_call():
    from investigator.assistant_binding import output_schema_for_steps
    job, api, bridge, model, steps = setup_runtime()
    model.provider.return_value.structured_output_mode = "json_object"
    with patch("investigator.holmes_adapter.ToolCallingLLM") as agent:
        def first_call(**kwargs):
            steps.append({"toolName": "get_incident_context", "state": "succeeded", "result": {"evidenceRefs": ["00000000-0000-4000-8000-000000000001"]}})
            return SimpleNamespace(result="invalid-json")
        replies = iter([first_call, lambda **kwargs: SimpleNamespace(result="{}")])
        agent.return_value.call.side_effect = lambda **kwargs: next(replies)(**kwargs)
        investigate(job, model, api, bridge)
        repair = agent.return_value.call.call_args.kwargs["messages"]
    content = next(m["content"] for m in repair if m.get("role") == "system" and "exact output schema" in m.get("content", ""))
    assert json.loads(content.split("fields. ", 1)[1]) == output_schema_for_steps(steps, job["assistant"]["taskKind"])
