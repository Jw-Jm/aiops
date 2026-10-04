import ipaddress
from pathlib import Path
from urllib.parse import urlsplit
from pydantic import BaseModel, ConfigDict, Field, field_validator

class ModelContract(BaseModel):
    model_config = ConfigDict(extra="forbid")
    base_url: str
    api_key_ref: str
    model: str = Field(min_length=1, max_length=128)
    timeout: int = Field(ge=1, le=120)
    token_budget: int = Field(ge=1, le=8192)

    @field_validator("model")
    @classmethod
    def admitted_model(cls,value):
        # SP06 admits only the verified 16k self-hosted Ollama protocol model.
        # Other context sizes or charging profiles need their own admission.
        if value != "llama3.1:8b-16k":
            raise ValueError("model context/cost profile has not been admitted")
        return value

    @field_validator("base_url")
    @classmethod
    def internal_endpoint(cls, value):
        url = urlsplit(value)
        if url.scheme not in ("http", "https") or url.username or url.password or url.query or url.fragment or not url.hostname:
            raise ValueError("invalid model endpoint")
        try:
            address = ipaddress.ip_address(url.hostname)
            internal = address.is_private or address.is_loopback
        except ValueError:
            # OrbStack's explicit development bridge requires its DNS Host
            # header; deployment separately pins the resolved exact host CIDR.
            internal = url.hostname.endswith(".svc.cluster.local") or url.hostname == "host.docker.internal"
        if not internal or url.path.rstrip("/") != "/v1":
            raise ValueError("an approved internal OpenAI-compatible endpoint is required")
        return value.rstrip("/")

    @field_validator("api_key_ref")
    @classmethod
    def secret_ref(cls, value):
        if value != "secret://model/api-key":
            raise ValueError("model credentials must use the mounted model Secret reference")
        return value

    def provider(self, llm_type, *, api_key_file="/run/secrets/model-api-key"):
        key = Path(api_key_file).read_text().strip()
        if not key:
            raise ValueError("model credential reference is unavailable")
        # Upstream DefaultLLM/LiteLLM owns provider construction and HTTP calls.
        return llm_type(model="openai/"+self.model, api_key=key, api_base=self.base_url,
                        args={"max_tokens":self.token_budget,"num_retries":0,
                              "custom_args":{"max_context_size":16384}})
