"""Explicit exception fixture; no provider text enters diagnostic categories."""
import unittest
from unittest.mock import patch
import httpx
from openai import APIConnectionError, APITimeoutError
from holmes.core.llm import DefaultLLM
from investigator.budget import BudgetedLLM, ModelCallError, _failure_kind

class Ledger:
 def __init__(self): self.calls=[]
 def allocate(self,*args):return {"stepId":"fixture-step"}
 def settle(self,*args):self.calls.append(args)

class CauseTests(unittest.TestCase):
 def test_wrapped_native_connection_and_timeout(self):
  for kind,cls in [("MODEL_UNAVAILABLE",APIConnectionError),("MODEL_TIMEOUT",APITimeoutError)]:
   native=cls(request=httpx.Request("POST","http://127.0.0.1:9/v1"))
   outer=RuntimeError("password=private");outer.__cause__=native
   self.assertEqual(_failure_kind(outer),kind)
   ledger=Ledger()
   llm=BudgetedLLM(model="openai/qwen2.5:1.5b",api_key="fixture",args={"max_tokens":128,"num_retries":0})
   llm.job_api=ledger
   with patch.object(DefaultLLM,"completion",side_effect=outer):
    with self.assertRaises(ModelCallError) as caught:llm.completion([{"role":"user","content":"fixture"}])
   self.assertEqual(str(caught.exception),kind)
   self.assertEqual(ledger.calls,[("fixture-step",None,None,"MODEL_FAILURE")])
 def test_unknown_cycle_is_bounded_and_does_not_classify_from_text(self):
  a=RuntimeError("MODEL_UNAVAILABLE password=private");b=RuntimeError("wrapped")
  a.__cause__=b;b.__cause__=a
  self.assertEqual(_failure_kind(a),"MODEL_FAILURE")
if __name__=="__main__":unittest.main()
