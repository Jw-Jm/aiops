from holmes.core.llm import DefaultLLM
from litellm import (
    APIConnectionError, AuthenticationError, BadRequestError,
    ContextWindowExceededError, ModelResponse, RateLimitError, Timeout,
)
import json
import hashlib
from .redaction import sanitize
from .prompt_binding import output_schema_for_steps


class ModelCallError(RuntimeError):
    """Static diagnostic categories; provider text and credentials are excluded."""
    def __init__(self, kind, provider_failure_kind=None):
        super().__init__(kind)
        self.kind = kind
        self.provider_failure_kind = provider_failure_kind


def _failure_kind(error):
    for exception, kind in (
        (Timeout, "MODEL_TIMEOUT"),
        (AuthenticationError, "MODEL_AUTHENTICATION_FAILED"),
        (ContextWindowExceededError, "MODEL_CONTEXT_LIMIT"),
        (RateLimitError, "MODEL_RATE_LIMITED"),
        (APIConnectionError, "MODEL_UNAVAILABLE"),
        (BadRequestError, "MODEL_REQUEST_REJECTED"),
    ):
        if isinstance(error, exception):
            return kind
    return "MODEL_FAILURE"


class BudgetedLLM(DefaultLLM):
    """Upstream LLM extension: admission surrounds, never replaces, completion."""
    job_api = None
    needs_initial_tools = True

    def completion(self, messages, *args, **kwargs):
        if self.job_api is None:
            raise RuntimeError("MODEL_ADMISSION_REQUIRED")
        # The locked local model has a 16k context cap. The entire cap is reserved
        # before calling upstream; unknown/error usage consumes the full bound.
        reserve={"modelRequests":1,"inputTokens":16384,"outputTokens":self.args["max_tokens"],"resultBytes":65536}
        if self.needs_initial_tools and kwargs.get("tools"):
            # Provider formatting boundary: allow the initial OpenAI tool call;
            # the upstream loop still owns execution and subsequent iterations.
            kwargs["response_format"]=None
            self.needs_initial_tools=False
        if kwargs.get("response_format"):
            # Official provider formatting extension; the original Holmes loop
            # still selects and executes tools. Only committed semantic Ledger
            # evidence IDs can appear in the formal result's reference array.
            kwargs["response_format"]={"type":"json_schema","json_schema":{"name":"InvestigationResult","strict":True,"schema":output_schema_for_steps(self.job_api.request("/steps"))}}
        request_hash="sha256:"+hashlib.sha256(json.dumps({"messages":messages,"arguments":args,"options":kwargs,"model":self.model,"maxTokens":self.args["max_tokens"]},sort_keys=True,separators=(",",":"),default=str).encode()).hexdigest()
        allocation=self.job_api.allocate("model",{"requestDigest":request_hash},True,reserve)
        if allocation.get("committed"):
            return ModelResponse(**allocation["result"]["response"])

        try:
            result=super().completion(messages,*args,**kwargs)
        except Exception as error:
            kind = _failure_kind(error)
            try:
                # The existing Go Contract conservatively charges unknown use.
                self.job_api.settle(allocation["stepId"],None,None,"MODEL_FAILURE")
            except Exception:
                raise ModelCallError("MODEL_FAILURE_SETTLEMENT_REJECTED", kind) from None
            raise ModelCallError(kind) from None
        usage=getattr(result,"usage",None)
        consumed=reserve.copy()
        if usage is not None:
            consumed["inputTokens"]=usage.prompt_tokens
            consumed["outputTokens"]=usage.completion_tokens
        try:
            self.job_api.settle(allocation["stepId"],{"provider":"openai-compatible","usageKnown":usage is not None,"response":sanitize(result.model_dump(mode="json"))},consumed)
        except Exception:
            # A successful provider response cannot be reclassified as a
            # provider failure or settled twice after a Ledger rejection.
            raise ModelCallError("MODEL_RESULT_SETTLEMENT_REJECTED") from None
        return result
