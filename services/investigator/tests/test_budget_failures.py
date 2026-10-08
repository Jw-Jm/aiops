"""Fault injection checks for the official completion extension, not live gates."""
import json

import pytest
from holmes.core.llm import DefaultLLM
from litellm import (
    APIConnectionError, AuthenticationError, BadRequestError,
    ContextWindowExceededError, ModelResponse, RateLimitError, Timeout,
)

from investigator.budget import BudgetedLLM


class RecordingLedger:
    def __init__(self, reject_settlement=False):
        self.reject_settlement = reject_settlement
        self.settlements = []

    def allocate(self, name, arguments, model, reserve):
        self.reserve = reserve
        return {"stepId": "admitted-model-step"}

    def settle(self, step_id, result, consumed=None, error_code=""):
        self.settlements.append((step_id, result, consumed, error_code))
        if self.reject_settlement:
            raise RuntimeError("JOB_API_REJECTED_409 token=do-not-log")


def client(ledger):
    llm = BudgetedLLM(model="openai/llama3.1:8b-16k", api_key="unit-input",
                      args={"max_tokens": 1024, "num_retries": 0})
    llm.job_api = ledger
    return llm


@pytest.mark.parametrize("exception,kind", [
    (Timeout("token=do-not-log", "llama3.1:8b-16k", "openai"), "MODEL_TIMEOUT"),
    (APIConnectionError("token=do-not-log", "openai", "llama3.1:8b-16k"), "MODEL_UNAVAILABLE"),
    (AuthenticationError("token=do-not-log", "openai", "llama3.1:8b-16k"), "MODEL_AUTHENTICATION_FAILED"),
    (ContextWindowExceededError("token=do-not-log", "llama3.1:8b-16k", "openai"), "MODEL_CONTEXT_LIMIT"),
    (RateLimitError("token=do-not-log", "openai", "llama3.1:8b-16k"), "MODEL_RATE_LIMITED"),
    (BadRequestError("token=do-not-log", "llama3.1:8b-16k", "openai"), "MODEL_REQUEST_REJECTED"),
    (RuntimeError("token=do-not-log"), "MODEL_FAILURE"),
])
def test_provider_failure_is_classified_without_leaking_exception(monkeypatch, exception, kind):
    def fail(*args, **kwargs):
        raise exception
    monkeypatch.setattr(DefaultLLM, "completion", fail)
    ledger = RecordingLedger()
    with pytest.raises(RuntimeError) as caught:
        client(ledger).completion([{"role": "user", "content": "bounded test"}])
    assert getattr(caught.value, "kind", None) == kind
    assert str(caught.value) == kind
    assert caught.value.__suppress_context__
    assert "do-not-log" not in str(caught.value)
    # Keep the existing Go Contract and its full-bound unknown-use settlement.
    assert ledger.settlements == [("admitted-model-step", None, None, "MODEL_FAILURE")]
    assert ledger.reserve["inputTokens"] == 16384
    assert ledger.reserve["outputTokens"] == 1024


def test_successful_provider_settlement_rejection_is_not_reclassified_or_settled_twice(monkeypatch):
    response = ModelResponse(choices=[{"message": {"role": "assistant", "content": "bounded"}}],
                             usage={"prompt_tokens": 8, "completion_tokens": 2, "total_tokens": 10})
    monkeypatch.setattr(DefaultLLM, "completion", lambda *args, **kwargs: response)
    ledger = RecordingLedger(reject_settlement=True)
    with pytest.raises(RuntimeError) as caught:
        client(ledger).completion([{"role": "user", "content": "bounded test"}])
    assert getattr(caught.value, "kind", None) == "MODEL_RESULT_SETTLEMENT_REJECTED"
    assert "do-not-log" not in str(caught.value)
    assert len(ledger.settlements) == 1
    assert ledger.settlements[0][3] == ""
    assert ledger.settlements[0][2]["inputTokens"] == 8


def test_provider_failure_settlement_rejection_preserves_safe_provider_category(monkeypatch):
    def fail(*args, **kwargs):
        raise Timeout("token=do-not-log", "llama3.1:8b-16k", "openai")
    monkeypatch.setattr(DefaultLLM, "completion", fail)
    ledger = RecordingLedger(reject_settlement=True)
    with pytest.raises(RuntimeError) as caught:
        client(ledger).completion([{"role": "user", "content": "bounded test"}])
    assert getattr(caught.value, "kind", None) == "MODEL_FAILURE_SETTLEMENT_REJECTED"
    assert getattr(caught.value, "provider_failure_kind", None) == "MODEL_TIMEOUT"
    assert "do-not-log" not in str(caught.value)
    assert len(ledger.settlements) == 1


def test_success_and_unknown_usage_keep_existing_contract_and_bounds(monkeypatch):
    response = ModelResponse(choices=[{"message": {"role": "assistant", "content": "bounded"}}])
    response.usage = None
    monkeypatch.setattr(DefaultLLM, "completion", lambda *args, **kwargs: response)
    ledger = RecordingLedger()
    assert client(ledger).completion([{"role": "user", "content": "bounded test"}]) is response
    _, summary, used, code = ledger.settlements[0]
    assert set(summary) == {"provider", "usageKnown", "response"}
    assert summary["usageKnown"] is False
    assert used == ledger.reserve
    assert code == ""
    assert "unit-input" not in json.dumps(summary)
