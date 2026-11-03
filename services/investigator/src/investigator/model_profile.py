"""Closed assistant profile; the legacy five-field ModelContract is unchanged."""
import hashlib
import ipaddress
import json
from pathlib import Path
from typing import Literal
from urllib.parse import urlsplit
from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator, model_serializer

class HostedEvidence(BaseModel):
    model_config=ConfigDict(extra="forbid",strict=True)
    provider: Literal['deepseek']
    protocol: Literal['openai-chat-completions']
    revisionPolicy: Literal['provider-managed-alias']
    documentationURL: Literal['https://api-docs.deepseek.com/']
    documentationDigest: str=Field(pattern=r'^sha256:[a-f0-9]{64}$')
    protocolEvidenceDigest: str=Field(pattern=r'^sha256:[a-f0-9]{64}$')
class AssistantModelProfile(BaseModel):
    model_config=ConfigDict(extra="forbid",strict=True)
    schemaVersion: Literal['assistant-model-profile/v1','assistant-model-profile/v2']
    version: str=Field(pattern=r'^[a-zA-Z0-9][a-zA-Z0-9._/-]{0,95}$')
    modelID: str=Field(pattern=r'^[a-zA-Z0-9][a-zA-Z0-9._:/-]{0,127}$')
    modelVersion: str=Field(pattern=r'^[a-zA-Z0-9][a-zA-Z0-9._:/-]{0,127}$')
    baseURL: str=Field(max_length=512)
    apiKeyRef: Literal['secret://model/api-key']
    contextWindow: int=Field(ge=1024,le=1000000)
    outputTokenLimit: int=Field(ge=1,le=8192)
    tokenAccounting: Literal['reserve-context-bound']
    costPolicy: Literal['zero-charge','provider-metered-token-bounded']
    toolCalling: bool
    structuredOutput: bool
    taskKinds: list[Literal['data_query','resource_troubleshooting','incident_followup']]=Field(min_length=1,max_length=3)
    timeoutSeconds: int=Field(ge=1,le=120)
    retries: int=Field(ge=0,le=0)
    licenseSPDX: str | None=Field(default=None,pattern=r'^[A-Za-z0-9][A-Za-z0-9.+-]{0,95}$')
    weightsDigest: str | None=Field(default=None,pattern=r'^sha256:[a-f0-9]{64}$')
    hosted: HostedEvidence | None=None
    evaluationVersion: str=Field(pattern=r'^[a-zA-Z0-9][a-zA-Z0-9._/-]{0,95}$')
    evaluationDigest: str=Field(pattern=r'^sha256:[a-f0-9]{64}$')
    admissionEvidenceDigest: str=Field(pattern=r'^sha256:[a-f0-9]{64}$')
    admissionState: Literal['pending','fixture_only','admitted']

    @model_serializer(mode='wrap')
    def serialize_material(self, handler):
        result=handler(self)
        for key in ('hosted','licenseSPDX','weightsDigest'):
            if result.get(key) is None:result.pop(key,None)
        return result

    @field_validator('toolCalling','structuredOutput')
    @classmethod
    def required_capability(cls,value):
        if not value:raise ValueError('required model capability unavailable')
        return value

    @model_validator(mode='after')
    def bounds(self):
        if self.outputTokenLimit>=self.contextWindow or len(set(self.taskKinds))!=len(self.taskKinds):raise ValueError('invalid profile bounds')
        if self.schemaVersion=='assistant-model-profile/v2':
            if self.baseURL!='https://api.deepseek.com/v1' or self.modelID!='deepseek-flash' or self.costPolicy!='provider-metered-token-bounded' or self.hosted is None or self.licenseSPDX is not None or self.weightsDigest is not None or self.admissionState=='fixture_only':
                raise ValueError('hosted model evidence/route/cost contract mismatch')
            return self
        if self.costPolicy!='zero-charge' or self.licenseSPDX is None or self.weightsDigest is None or self.hosted is not None:
            raise ValueError('self-hosted model evidence/cost contract mismatch')
        u=urlsplit(self.baseURL)
        if u.scheme not in ('http','https') or u.username is not None or u.password is not None or u.query or u.fragment or u.path!='/v1' or '?' in self.baseURL or '#' in self.baseURL or not u.hostname:raise ValueError('invalid profile endpoint')
        try:internal=ipaddress.ip_address(u.hostname).is_private or ipaddress.ip_address(u.hostname).is_loopback
        except ValueError:internal=u.hostname.endswith('.svc.cluster.local')
        if not internal:raise ValueError('profile endpoint must be private')
        return self

    def reference(self):
        # Match Go encoding/json for this closed JSON material, including its
        # HTML and Unicode line separator escaping. Fields are required.
        raw=json.dumps(self.model_dump(),sort_keys=True,separators=(',',':'),ensure_ascii=False)
        for char,escaped in (('&',r'\u0026'),('<',r'\u003c'),('>',r'\u003e'),('\u2028',r'\u2028'),('\u2029',r'\u2029')):raw=raw.replace(char,escaped)
        return self.version+'@sha256:'+hashlib.sha256(raw.encode()).hexdigest()

    def usable(self, listen):
        if self.admissionState=='admitted':return True
        if self.admissionState!='fixture_only':return False
        try:return ipaddress.ip_address(listen.rsplit(':',1)[0].strip('[]')).is_loopback and ipaddress.ip_address(urlsplit(self.baseURL).hostname).is_loopback
        except ValueError:return False

    def provider(self,llm_type,*,api_key_file):
        key=Path(api_key_file).read_text().strip()
        if not key:raise ValueError('model credential reference is unavailable')
        args={'max_tokens':self.outputTokenLimit,'num_retries':0,
              'custom_args':{'max_context_size':self.contextWindow}}
        if self.hosted is not None:args['extra_body']={'thinking':{'type':'disabled'}}
        provider=llm_type(model='openai/'+self.modelID,api_key=key,api_base=self.baseURL,args=args)
        if self.hosted is not None:provider.structured_output_mode='json_object'
        return provider
