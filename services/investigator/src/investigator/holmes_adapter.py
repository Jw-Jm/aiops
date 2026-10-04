import asyncio
import json
from pydantic import PrivateAttr
from mcp import ClientSession
from mcp.client.streamable_http import streamable_http_client
from holmes.core.tools import Tool, Toolset, ToolsetStatusEnum, StructuredToolResult, StructuredToolResultStatus
from holmes.core.tools_utils.tool_executor import ToolExecutor
from holmes.core.tool_calling_llm import ToolCallingLLM
from .budget import BudgetedLLM
from .prompt_binding import messages, OUTPUT_JSON_SCHEMA
from .redaction import sanitize

class PlatformTool(Tool):
    _schema: dict = PrivateAttr()
    _job_api: object = PrivateAttr()
    _bridge: object = PrivateAttr()

    def get_openai_format(self):
        return {"type":"function","function":{"name":self.name,"description":self.description,"parameters":self._schema}}

    def get_parameterized_one_liner(self, params):
        return self.name

    def _invoke(self, params, context):
        allocation=self._job_api.allocate(self.name,params)
        if allocation.get("committed"):
            result=self._job_api.request("/steps/"+allocation["stepId"]+"/result?argsDigest="+allocation["argsDigest"])
        else:
            result=self._bridge.call(self.name,params,allocation)
        return StructuredToolResult(status=StructuredToolResultStatus.ERROR if result.get("state")=="failed" else StructuredToolResultStatus.SUCCESS,
                                    data={"untrusted_tool_result":sanitize(result)})

class MCPBridge:
    def __init__(self, endpoint, token, http_client):
        self.endpoint=endpoint
        self._token=token
        self._client=http_client
        self._runner=asyncio.Runner()
        self.token_provider=lambda: self._token

    async def _request(self, name=None, params=None, allocation=None):
        self._client.headers["Authorization"]="Bearer "+self.token_provider()
        async with streamable_http_client(self.endpoint, http_client=self._client) as (read,write,_):
            async with ClientSession(read,write) as session:
                initialized=await session.initialize()
                if initialized.protocolVersion != "2025-06-18":
                    raise RuntimeError("MCP_PROTOCOL_UNVERIFIED")
                if name is None:
                    return (await session.list_tools()).tools
                result=await session.call_tool(name,params,meta={"ops/stepId":allocation["stepId"],"ops/jti":allocation["jti"]})
                if result.structuredContent is None:
                    raise RuntimeError("MCP_STRUCTURED_RESULT_REQUIRED")
                return result.structuredContent

    def close(self):
        self._runner.run(self._client.aclose())
        self._runner.close()

    def catalog(self):return self._runner.run(self._request())
    def call(self,name,params,allocation):return self._runner.run(self._request(name,params,allocation))

def investigate(job, model, job_api, bridge, api_key_file="/run/secrets/model-api-key"):
    tools=[]
    for definition in bridge.catalog():
        if definition.name=="query_kubevirt":continue
        tool=PlatformTool(name=definition.name,description=definition.description or definition.name)
        tool._schema=definition.inputSchema
        tool._job_api=job_api
        tool._bridge=bridge
        tools.append(tool)
    toolset=Toolset(name="platform",description="Platform read-only semantic MCP",tools=tools,
                    enabled=True,status=ToolsetStatusEnum.ENABLED)
    llm=model.provider(BudgetedLLM,api_key_file=api_key_file)
    llm.job_api=job_api
    agent=ToolCallingLLM(tool_executor=ToolExecutor([toolset]),max_steps=8,llm=llm,tool_results_dir=None)
    steps=job_api.request("/steps")
    prior=[step["result"] for step in steps if step["state"]=="succeeded" and step["toolName"]!="model"]
    llm.needs_initial_tools=not bool(prior)
    result=agent.call(messages=messages(job,prior),response_format={"type":"json_schema","json_schema":{"name":"InvestigationResult","strict":True,"schema":OUTPUT_JSON_SCHEMA}})
    try:
        proposal=json.loads(result.result)
    except (ValueError,TypeError):
        raise RuntimeError("INVALID_MODEL_JSON") from None
    return sanitize(proposal)
