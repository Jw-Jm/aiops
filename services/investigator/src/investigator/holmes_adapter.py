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

from .mcp_bridge import MCPBridge

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
    from .assistant_binding import is_assistant, messages as assistant_messages, output_schema as assistant_schema
    assistant=is_assistant(job)
    llm.assistant_task=assistant
    if assistant:llm.assistant_task_kind=job["assistant"]["taskKind"]
    prompt=assistant_messages(job,prior) if assistant else messages(job,prior)
    schema=assistant_schema() if assistant else OUTPUT_JSON_SCHEMA
    response_format={"type":"json_schema","json_schema":{"name":"AssistantAnswer" if assistant else "InvestigationResult","strict":True,"schema":schema}}
    result=agent.call(messages=prompt,response_format=response_format)
    try:
        proposal=json.loads(result.result)
    except (ValueError,TypeError):
        if not assistant:raise RuntimeError("INVALID_MODEL_JSON") from None
        # At most one repair, charged by the same BudgetedLLM/Job admissions.
        # The invalid model text is not promoted into trusted context.
        repair_steps=job_api.request("/steps")
        repair_prior=[step["result"] for step in repair_steps if step["state"]=="succeeded" and step["toolName"]!="model"]
        repair_prompt=assistant_messages(job,repair_prior)+[{"role":"user","content":"The previous response was not valid JSON. Return one object matching the required schema. Use only already committed tool references."}]
        repaired=agent.call(messages=repair_prompt,response_format=response_format)
        try:proposal=json.loads(repaired.result)
        except (ValueError,TypeError):raise RuntimeError("INVALID_MODEL_JSON") from None
    return sanitize(proposal)
