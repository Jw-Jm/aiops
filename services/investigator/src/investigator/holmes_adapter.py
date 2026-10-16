import asyncio
import json
from jsonschema import ValidationError
from pydantic import PrivateAttr
from mcp import ClientSession
from mcp.client.streamable_http import streamable_http_client
from holmes.core.tools import Tool, Toolset, ToolsetStatusEnum, StructuredToolResult, StructuredToolResultStatus
from holmes.core.tools_utils.tool_executor import ToolExecutor
from holmes.core.tool_calling_llm import ToolCallingLLM
from .budget import BudgetedLLM, ModelCallError
from .prompt_binding import messages, OUTPUT_JSON_SCHEMA
from .redaction import sanitize

class PlatformTool(Tool):
    _schema: dict = PrivateAttr()
    _job_api: object = PrivateAttr()
    _bridge: object = PrivateAttr()
    _task_kind: str = PrivateAttr(default="")

    def get_openai_format(self):
        parameters={key:value for key,value in self._schema.items() if key not in ("$id","$schema","x-tool-contract")}
        if self._task_kind=="data_query" and self.name=="execute_analysis_query":
            # Query scope is already frozen; resource overrides belong to investigations.
            parameters["properties"]={key:value for key,value in parameters.get("properties",{}).items() if key not in ("canonicalId","windowRole")}
        # Legacy output metadata references private contracts, not tool inputs.
        if "x-tool-contract" in self._schema and "$defs" in parameters:
            definitions={key:value for key,value in parameters["$defs"].items() if key!="output"}
            if definitions:parameters["$defs"]=definitions
            else:parameters.pop("$defs")
        return {"type":"function","function":{"name":self.name,"description":self.description,"parameters":parameters}}

    def get_parameterized_one_liner(self, params):
        return self.name

    def _invoke(self, params, context):
        allocation=self._job_api.allocate(self.name,params)
        if allocation.get("committed"):
            result=self._job_api.request("/steps/"+allocation["stepId"]+"/result?argsDigest="+allocation["argsDigest"])
        else:
            result=self._bridge.call(self.name,params,allocation)
        from .assistant_binding import model_tool_result
        return StructuredToolResult(status=StructuredToolResultStatus.ERROR if result.get("state")=="failed" else StructuredToolResultStatus.SUCCESS,
                                    data={"untrusted_tool_result":model_tool_result(self.name, result)})

from .mcp_bridge import MCPBridge

def investigate(job, model, job_api, bridge, api_key_file="/run/secrets/model-api-key"):
    tools=[]
    for definition in bridge.catalog():
        if definition.name=="query_kubevirt":continue
        tool=PlatformTool(name=definition.name,description=definition.description or definition.name)
        tool._schema=definition.inputSchema
        tool._job_api=job_api
        tool._bridge=bridge
        tool._task_kind=job.get("assistant",{}).get("taskKind","")
        tools.append(tool)
    toolset=Toolset(name="platform",description="Platform read-only semantic MCP",tools=tools,
                    enabled=True,status=ToolsetStatusEnum.ENABLED)
    from .assistant_binding import is_assistant, messages as assistant_messages, output_schema as assistant_schema, committed_reads, query_required, required_query_tool, evidence_reads_required
    assistant=is_assistant(job)
    steps=job_api.request("/steps")
    if assistant and not any(step["state"] == "succeeded" and step["toolName"] == "get_assistant_context" for step in steps):
        # The model may ignore named tool_choice; ground it through the same
        # admitted MCP/Step path before asking it to interpret the request.
        context_tool=next((tool for tool in tools if tool.name == "get_assistant_context"), None)
        if context_tool is None: raise RuntimeError("INITIAL_CONTEXT_TOOL_UNAVAILABLE")
        result=context_tool._invoke({}, None)
        if result.status != StructuredToolResultStatus.SUCCESS: raise RuntimeError("ASSISTANT_CONTEXT_UNAVAILABLE")
        steps=job_api.request("/steps")
        if not any(step["state"] == "succeeded" and step["toolName"] == "get_assistant_context" for step in steps):
            raise RuntimeError("ASSISTANT_CONTEXT_UNAVAILABLE")
    if assistant:
        context=next(step["result"].get("data",{}) for step in steps if step["state"]=="succeeded" and step["toolName"]=="get_assistant_context")
        reads=[]
        if evidence_reads_required(context) and job["assistant"]["taskKind"]=="incident_followup":
            reads=[("get_incident_context",{"incidentId":job["incidentId"]}),("get_formal_references",{})]
        request=context.get("faultChainEvaluationRequest")
        if evidence_reads_required(context) and request and job["assistant"].get("catalogVersion")=="assistant-tools/v2":
            from .fault_chain import primary_resource_requests
            reads.extend(primary_resource_requests(request["targetCanonicalId"]))
        for name,args in reads:
            tool=next((tool for tool in tools if tool.name==name),None)
            if tool is None:raise RuntimeError("REQUIRED_READ_TOOL_UNAVAILABLE")
            # Allocation reuses committed reads on takeover; a new turn has a new Job.
            tool._invoke(args,None)
        steps=job_api.request("/steps")
    prior=committed_reads(steps) if assistant else [step["result"] for step in steps if step["state"]=="succeeded" and step["toolName"]!="model"]
    llm=model.provider(BudgetedLLM,api_key_file=api_key_file)
    llm.job_api=job_api
    agent=ToolCallingLLM(tool_executor=ToolExecutor([toolset]),max_steps=8,llm=llm,tool_results_dir=None)
    llm.needs_initial_tools=not bool(prior)
    llm.assistant_task=assistant
    llm.requires_query=assistant and job["assistant"]["taskKind"]=="data_query" and query_required(context)
    if assistant:llm.assistant_task_kind=job["assistant"]["taskKind"]
    prompt=assistant_messages(job,prior) if assistant else messages(job,prior)
    schema=assistant_schema() if assistant else OUTPUT_JSON_SCHEMA
    response_format={"type":"json_schema","json_schema":{"name":"AssistantAnswer" if assistant else "InvestigationResult","strict":True,"schema":schema}}
    result=agent.call(messages=prompt,response_format=response_format)
    def decode(text):
        proposal=json.loads(text)
        if assistant:
            from .assistant_binding import validate_proposal
            validate_proposal(proposal,job_api.request("/steps"),job["assistant"]["taskKind"])
        return proposal
    try:
        proposal=decode(result.result)
    except (ValueError,TypeError,ValidationError):
        if not assistant:raise RuntimeError("INVALID_MODEL_JSON") from None
        # At most one repair, charged by the same BudgetedLLM/Job admissions.
        # The invalid model text is not promoted into trusted context.
        repair_steps=job_api.request("/steps")
        repair_prior=committed_reads(repair_steps)
        repair_prompt=assistant_messages(job,repair_prior)+[{"role":"user","content":"The previous response was invalid JSON or violated the output/reference schema. Return one object matching the required schema. resourceSourceSteps must be [], faultChainContextRef must be null: the platform attaches its own references. Use only permitted committed references. Historical checks are not checks executed in this Job. Do not copy platform-owned fields from prior answers."}]
        repaired=agent.call(messages=repair_prompt,response_format=response_format)
        try:proposal=decode(repaired.result)
        except (ValueError,TypeError,ValidationError):raise ModelCallError("MODEL_OUTPUT_INVALID") from None
    if llm.requires_query and required_query_tool(job_api.request("/steps")):
        proposal["partial"]=True
        proposal["warnings"]=list(dict.fromkeys([*proposal.get("warnings",[]),"query_not_executed"]))
    return sanitize(proposal)
