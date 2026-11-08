"""Output constraints follow committed tool reads across the Holmes loop."""
import json
from unittest.mock import patch

import pytest
from holmes.core.llm import DefaultLLM
from litellm import ModelResponse

from investigator.budget import BudgetedLLM
from investigator.prompt_binding import output_schema_for_steps as legacy_schema
from investigator.assistant_binding import output_schema_for_steps as assistant_schema


PREFIX = "Return the final JSON object matching this exact output schema. Do not add or rename fields. "


@pytest.mark.parametrize("assistant", [False, True])
def test_json_contract_refreshes_after_tools_without_mutating_history(assistant):
    class Ledger:
        def __init__(self):
            self.steps = []
            self.digests = []

        def request(self, path):
            assert path == "/steps"
            return self.steps

        def allocate(self, name, arguments, model, reserve):
            self.digests.append(arguments["requestDigest"])
            return {"stepId": "model-step"}

        def settle(self, *args):
            pass

    ledger = Ledger()
    llm = BudgetedLLM(model="openai/unit", api_key="unit-input", args={"max_tokens": 1024})
    llm.job_api = ledger
    llm.assistant_task = assistant
    llm.assistant_task_kind = "incident_followup"
    llm.structured_output_mode = "json_object"
    llm.needs_initial_tools = False
    history = [{"role": "system", "content": PREFIX + "{}"},
               {"role": "user", "content": "Explain the committed evidence."}]
    response = ModelResponse(choices=[{"message": {"role": "assistant", "content": "{}"}}])
    with patch.object(DefaultLLM, "completion", return_value=response) as provider:
        for refs in [[], ["00000000-0000-4000-8000-000000000001"]]:
            ledger.steps = [{"toolName": "get_incident_context", "state": "succeeded", "result": {"evidenceRefs": refs}}]
            llm.completion(history, response_format={"type": "json_schema"})
            sent = provider.call_args.args[0]
            contracts = [m["content"] for m in sent if m.get("role") == "system" and m["content"].startswith(PREFIX)]
            assert len(contracts) == 1
            schema = json.loads(contracts[0][len(PREFIX):])
            assert schema == (assistant_schema(ledger.steps, "incident_followup") if assistant else legacy_schema(ledger.steps))
            assert sent[-2] == history[-1]
            assert provider.call_args.kwargs["response_format"] == {"type": "json_object"}
    assert ledger.digests[0] != ledger.digests[1]
    assert history[0]["content"] == PREFIX + "{}"
