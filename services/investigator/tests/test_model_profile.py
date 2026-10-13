import json
import os
from pathlib import Path
import pytest
from investigator.model_profile import AssistantModelProfile

def material():
    # Tests run in the isolated container; fixture is shared with Go/Helm.
    path=os.environ.get('OPS_ASSISTANT_PROFILE_FIXTURE')
    if path is None:path=Path(__file__).resolve().parents[3]/'test/fixtures/assistant/model-profile.fixture.json'
    return json.loads(Path(path).read_text())

def test_profile_digest_and_fixture_boundary():
    p=AssistantModelProfile.model_validate(material())
    assert p.reference()=='assistant-smoke/v1@sha256:ef4cbe7d6c2dd7bfa40965887891c8f17a234a4689961fe8964391ec7271dd66'
    assert p.usable('127.0.0.1:8083')
    assert not p.usable('0.0.0.0:8083')
    assert not p.usable(':8083')
    old=p.reference();p.contextWindow=32768
    assert p.reference()!=old
    p.admissionState='pending'
    assert not p.usable('127.0.0.1:8083')

def test_profile_closed_bounds_and_capabilities():
    for key,value in [('costPolicy','paid'),('retries',1),('retries',False),('baseURL','https://example.com/v1'),('apiKeyRef','secret://other/key'),('taskKinds',['data_query','data_query']),('contextWindow',True),('toolCalling',False),('toolCalling',1),('structuredOutput',1),('outputTokenLimit',16384),('unknown','injected')]:
        obj=material();obj[key]=value
        with pytest.raises(ValueError):AssistantModelProfile.model_validate(obj)

def test_provider_uses_profile_context_output_and_zero_retries(tmp_path):
    class Provider:
        def __init__(self,**kwargs):self.arguments=kwargs
    key=tmp_path/'key';key.write_text('unit-key')
    p=AssistantModelProfile.model_validate(material());p.contextWindow=32768
    provider=p.provider(Provider,api_key_file=str(key))
    assert provider.arguments['args']=={'max_tokens':2048,'num_retries':0,'custom_args':{'max_context_size':32768}}
    assert provider.arguments['model']=='openai/qwen2.5:1.5b'

def test_runtime_rejects_missing_changed_or_unapproved_profile():
    from investigator.runtime_config import RuntimeConfig
    p=AssistantModelProfile.model_validate(material())
    value=dict(listenAddress='127.0.0.1:8086',jobAPI='https://127.0.0.1:8083',mcpEndpoint='https://127.0.0.1:8083/mcp',apiServerName='ops-api.fixture.svc.cluster.local',caFile='/trust',crlFile='/crl',certificateFile='/cert',privateKeyFile='/key',workerIdentity='spiffe://ops.local/ns/fixture/sa/ops-worker',modelAPIKeyFile='/model-key',assistantModelProfileVersion=p.reference(),assistantModelProfile=p.model_dump(),model=dict(base_url=p.baseURL,api_key_ref=p.apiKeyRef,model=p.modelID,timeout=120,token_budget=2048))
    assert RuntimeConfig.model_validate(value).checked()
    for change in ('missing','changed','fixture_external_listener','pending','timeout'):
        import copy
        wrong=copy.deepcopy(value)
        if change=='missing':wrong.pop('assistantModelProfile')
        elif change=='changed':wrong['assistantModelProfile']['contextWindow']=32768
        elif change=='fixture_external_listener':wrong['listenAddress']='0.0.0.0:8086'
        elif change=='timeout':
            wrong['assistantModelProfile']['timeoutSeconds']=60
            wrong['assistantModelProfileVersion']=AssistantModelProfile.model_validate(wrong['assistantModelProfile']).reference()
        else:wrong['assistantModelProfile']['admissionState']='pending'
        with pytest.raises(ValueError):RuntimeConfig.model_validate(wrong).checked()

def test_profile_uses_real_holmes_formatting_without_duplicate_timeout(tmp_path):
    from unittest.mock import patch
    from investigator.budget import BudgetedLLM
    from litellm import ModelResponse
    class Ledger:
        def allocate(self,name,args,model,reserve):self.reserve=reserve;return {'stepId':'profile-unit'}
        def settle(self,*args):pass
    p=AssistantModelProfile.model_validate(material());p.contextWindow=32768
    key=tmp_path/'key';key.write_text('unit-key')
    llm=p.provider(BudgetedLLM,api_key_file=str(key));llm.job_api=Ledger();llm.needs_initial_tools=False
    response=ModelResponse(choices=[{'message':{'role':'assistant','content':'bounded'}}])
    with patch('holmes.core.llm.litellm.completion',return_value=response) as provider:
        llm.completion([])
        assert provider.call_args.kwargs['num_retries']==0
        assert provider.call_args.kwargs['max_tokens']==2048
        assert 'timeout' in provider.call_args.kwargs
        assert 'timeout' not in llm.args
        assert llm.job_api.reserve['inputTokens']==32768
