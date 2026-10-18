"""Provider formatting regression, runnable in the admitted native runtime."""
import unittest
from unittest.mock import patch
from holmes.core.llm import DefaultLLM
from litellm import ModelResponse
from investigator.budget import BudgetedLLM, ModelCallError

class Ledger:
    def __init__(self): self.settlements=[]; self.allocations=[]
    def allocate(self,name,arguments,model,reserve):
        self.allocations.append((name,arguments,model,reserve))
        return {"stepId":"unit-model-step"}
    def settle(self,*args): self.settlements.append(args)
    def request(self,suffix):
        assert suffix=="/steps"
        return []

class InitialToolChoiceTests(unittest.TestCase):
    def test_frozen_numeric_request_requires_real_query_then_restores_auto(self):
        llm=self.client();llm.assistant_task=True;llm.needs_initial_tools=False;llm.requires_query=True
        steps=[];llm.job_api.request=lambda path:steps
        tools=[{"type":"function","function":{"name":n,"parameters":{"type":"object"}}} for n in ("describe_analysis_dataset","execute_analysis_query")]
        response=ModelResponse(choices=[{"message":{"role":"assistant","content":"bounded"}}])
        with patch.object(DefaultLLM,"completion",return_value=response) as provider:
            llm.completion([],tools=tools,tool_choice="auto")
            self.assertEqual(provider.call_args.kwargs["tool_choice"]["function"]["name"],"describe_analysis_dataset")
            steps.append({"toolName":"describe_analysis_dataset","state":"succeeded","result":{"data":{"datasets":[{"requiredFilters":[]}]}}})
            llm.completion([],tools=tools,tool_choice="auto")
            self.assertEqual(provider.call_args.kwargs["tool_choice"]["function"]["name"],"execute_analysis_query")
            steps.append({"toolName":"execute_analysis_query","state":"failed"})
            llm.completion([],tools=tools,tool_choice="auto")
            self.assertEqual(provider.call_args.kwargs["tool_choice"],"auto")
        self.assertEqual(len(llm.job_api.allocations),3)

    def client(self):
        llm=BudgetedLLM(model="openai/llama3.1:8b-16k",api_key="unit-input",args={"max_tokens":1024,"num_retries":0})
        llm.job_api=Ledger()
        return llm
    def test_profile_context_bound_is_reserved_and_unknown_usage_is_charged(self):
        for bound, output in ((16384, 2048), (131072, 4096), (1000000, 8192)):
            with self.subTest(context=bound):
                llm=self.client();llm.needs_initial_tools=False
                llm.max_context_size=bound
                llm.args["max_tokens"]=output
                self.assertEqual(llm.get_maximum_output_token(),output)
                self.assertEqual(llm.get_context_window_size(),bound)
                response=ModelResponse(choices=[{"message":{"role":"assistant","content":"bounded"}}])
                with patch.object(DefaultLLM,"completion",return_value=response):llm.completion([])
                self.assertEqual(llm.job_api.allocations[0][3]["inputTokens"],bound)
                self.assertEqual(llm.job_api.settlements[0][2]["inputTokens"],bound)
                self.assertEqual(llm.job_api.allocations[0][3]["outputTokens"],output)
                self.assertEqual(llm.job_api.settlements[0][2]["outputTokens"],output)
    def test_initial_choice_then_upstream_auto(self):
        response=ModelResponse(choices=[{"message":{"role":"assistant","content":"bounded"}}],usage={"prompt_tokens":8,"completion_tokens":2,"total_tokens":10})
        llm=self.client();tools=[{"type":"function","function":{"name":"get_incident_context","parameters":{"type":"object"}}}]
        with patch.object(DefaultLLM,"completion",return_value=response) as provider:
            llm.completion([],tools=tools,response_format={"type":"json_schema"},tool_choice="auto")
            self.assertEqual(provider.call_args.kwargs["tool_choice"],{"type":"function","function":{"name":"get_incident_context"}})
            self.assertIsNone(provider.call_args.kwargs["response_format"])
            self.assertEqual(len(llm.job_api.allocations),1)
            self.assertEqual(len(llm.job_api.settlements),1)
            llm.completion([],tools=tools,tool_choice="auto")
            self.assertEqual(provider.call_args.kwargs["tool_choice"],"auto")
    def test_assistant_uses_its_own_context_and_answer_contract(self):
        response=ModelResponse(choices=[{"message":{"role":"assistant","content":"bounded"}}])
        llm=self.client();llm.assistant_task=True
        tools=[{"type":"function","function":{"name":"get_assistant_context","parameters":{"type":"object"}}}]
        with patch.object(DefaultLLM,"completion",return_value=response) as provider:
            llm.completion([],tools=tools,response_format={"type":"json_schema"},tool_choice="auto")
            self.assertEqual(provider.call_args.kwargs["tool_choice"]["function"]["name"],"get_assistant_context")
            self.assertIsNone(provider.call_args.kwargs["response_format"])
            llm.completion([],tools=tools,response_format={"type":"json_schema"},tool_choice="auto")
            contract=provider.call_args.kwargs["response_format"]["json_schema"]
            self.assertEqual(contract["name"],"AssistantAnswer")
            self.assertIn("answerKind",contract["schema"]["properties"])
            self.assertNotIn("incidentId",contract["schema"]["properties"])

    def test_assistant_cannot_start_with_incident_context(self):
        llm=self.client();llm.assistant_task=True
        with patch.object(DefaultLLM,"completion") as provider:
            with self.assertRaisesRegex(ModelCallError,"INITIAL_CONTEXT_TOOL_UNAVAILABLE"):
                llm.completion([],tools=[{"type":"function","function":{"name":"get_incident_context"}}])
            provider.assert_not_called()
            self.assertEqual(llm.job_api.allocations,[])

    def test_missing_context_tool_fails_before_provider_and_admission(self):
        llm=self.client()
        with patch.object(DefaultLLM,"completion") as provider:
            with self.assertRaisesRegex(ModelCallError,"INITIAL_CONTEXT_TOOL_UNAVAILABLE"):
                llm.completion([],tools=[{"type":"function","function":{"name":"foreign"}}])
            provider.assert_not_called()
            self.assertEqual(llm.job_api.allocations,[])
            self.assertEqual(llm.job_api.settlements,[])
    def test_named_choice_reaches_real_holmes_provider_and_args_are_restored(self):
        response=ModelResponse(choices=[{"message":{"role":"assistant","content":"bounded"}}])
        llm=self.client();original=llm.args
        tools=[{"type":"function","function":{"name":"get_incident_context","parameters":{"type":"object"}}}]
        with patch("holmes.core.llm.litellm.completion",return_value=response) as provider:
            llm.completion([],tools=tools,tool_choice="auto")
            self.assertEqual(provider.call_args.kwargs["tools"],tools)
            self.assertEqual(provider.call_args.kwargs["tool_choice"],{"type":"function","function":{"name":"get_incident_context"}})
            self.assertIs(llm.args,original)
            self.assertNotIn("tools",llm.args)
            llm.completion([],tools=tools,tool_choice="auto")
            self.assertEqual(provider.call_args.kwargs["tool_choice"],"auto")
    def test_provider_failure_restores_model_arguments(self):
        llm=self.client();original=llm.args
        tools=[{"type":"function","function":{"name":"get_incident_context"}}]
        with patch("holmes.core.llm.litellm.completion",side_effect=RuntimeError("unit-failure")):
            with self.assertRaises(ModelCallError):
                llm.completion([],tools=tools,tool_choice="auto")
        self.assertIs(llm.args,original)
    def test_resumed_semantic_context_preserves_upstream_choice(self):
        response=ModelResponse(choices=[{"message":{"role":"assistant","content":"bounded"}}])
        llm=self.client();llm.needs_initial_tools=False
        with patch.object(DefaultLLM,"completion",return_value=response) as provider:
            llm.completion([],tools=[{"type":"function","function":{"name":"query_metrics"}}],tool_choice="auto")
            self.assertEqual(provider.call_args.kwargs["tool_choice"],"auto")

if __name__=="__main__": unittest.main(verbosity=2)
