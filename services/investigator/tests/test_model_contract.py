import pytest
from investigator.model_config import ModelContract

def test_closed_model_contract():
    model = ModelContract(base_url="http://127.0.0.1:11434/v1", api_key_ref="secret://model/api-key", model="llama3.1:8b-16k", timeout=20, token_budget=1024)
    assert model.model == "llama3.1:8b-16k"
    with pytest.raises(ValueError):
        ModelContract(**{**model.model_dump(), "provider":"other"})
    for url in ["http://example.org/v1", "file:///etc/passwd", "https://user:password@127.0.0.1/v1"]:
        with pytest.raises(ValueError):
            ModelContract(**{**model.model_dump(), "base_url":url})
    assert ModelContract(**{**model.model_dump(), "base_url":"http://host.docker.internal:11434/v1"}).base_url == "http://host.docker.internal:11434/v1"
    with pytest.raises(ValueError):
        ModelContract(**{**model.model_dump(), "base_url":"http://arbitrary.internal:11434/v1"})

def test_unadmitted_context_and_cost_profile_is_rejected():
    with pytest.raises(ValueError):
        ModelContract(base_url="http://127.0.0.1:11434/v1", api_key_ref="secret://model/api-key", model="unadmitted-model", timeout=20, token_budget=1024)
