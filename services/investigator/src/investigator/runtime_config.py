"""Closed workload routing configuration; Model Contract remains five fields."""
from pydantic import BaseModel, ConfigDict, field_validator
from urllib.parse import urlsplit
import ipaddress
from typing import Literal
from .model_config import ModelContract
from .model_profile import AssistantModelProfile

class RuntimeConfig(BaseModel):
    model_config = ConfigDict(extra="forbid")
    listenAddress: str
    jobAPI: str
    mcpEndpoint: str
    apiServerName: str
    caFile: str
    crlFile: str
    certificateFile: str
    privateKeyFile: str
    identityMode: Literal["file","openbao-kubernetes"]="file"
    workerIdentity: str
    modelAPIKeyFile: str
    assistantModelProfileVersion: str | None = None
    assistantModelProfile: AssistantModelProfile | None = None
    model: ModelContract

    @field_validator("jobAPI","mcpEndpoint")
    @classmethod
    def workload_route(cls,value):
        u=urlsplit(value)
        if u.scheme!="https" or u.username or u.password or u.query or u.fragment or u.path not in ("","/mcp"):
            raise ValueError("invalid workload route")
        try: internal=ipaddress.ip_address(u.hostname).is_private or ipaddress.ip_address(u.hostname).is_loopback
        except ValueError: internal=u.hostname is not None and u.hostname.endswith(".svc.cluster.local")
        if not internal: raise ValueError("workload route must be private")
        return value

    def checked(self):
        u=urlsplit(self.jobAPI)
        m=urlsplit(self.mcpEndpoint)
        if u.path!="" or m.path!="/mcp" or (u.hostname,u.port)!=(m.hostname,m.port):
            raise ValueError("MCP must use the same approved API workload")
        suffix=".svc.cluster.local"
        if not self.apiServerName.startswith("ops-api.") or not self.apiServerName.endswith(suffix):raise ValueError("invalid API workload")
        namespace=self.apiServerName[len("ops-api."):-len(suffix)]
        if self.workerIdentity!="spiffe://ops.local/ns/"+namespace+"/sa/ops-worker":raise ValueError("worker identity mismatch")
        if self.assistantModelProfileVersion is not None:
            p=self.assistantModelProfile
            if p is None or not p.usable(self.listenAddress) or p.reference()!=self.assistantModelProfileVersion:
                raise ValueError("assistant model profile is unavailable or changed")
            if p.timeoutSeconds!=self.model.timeout:raise ValueError("profile timeout must match the shared locked Holmes runtime")
            if p.baseURL!=self.model.base_url:raise ValueError("assistant must use the approved model egress endpoint")
        elif self.assistantModelProfile is not None:raise ValueError("assistant profile reference is required")
        return self.model_dump()
