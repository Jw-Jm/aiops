from unittest.mock import Mock
import httpx
import pytest
from investigator.holmes_adapter import PlatformTool


def tool(api,bridge):
    value=PlatformTool(name="execute_analysis_query",description="Read persisted facts")
    value._job_api=api;value._bridge=bridge
    return value


def test_lost_mcp_reply_recovers_only_the_exact_committed_receipt():
    api=Mock();bridge=Mock()
    api.allocate.return_value={"stepId":"fixed-step","argsDigest":"sha256:fixed"}
    receipt={"state":"succeeded","data":{"queryResultRefs":["fixed-result"]}}
    api.request.return_value=receipt
    bridge.call.side_effect=ExceptionGroup("transport",[httpx.ReadTimeout("private endpoint and credentials")])
    result=tool(api,bridge)._invoke({"datasetId":"resources.current"},None)
    api.allocate.assert_called_once()
    bridge.call.assert_called_once()
    api.request.assert_called_once_with("/steps/fixed-step/result?argsDigest=sha256:fixed")
    assert "fixed-result" in str(result.data)


@pytest.mark.parametrize("failure",["JOB_API_REJECTED_403","JOB_API_REJECTED_404"])
def test_missing_or_no_longer_authorized_receipt_never_reinvokes_tool(failure):
    api=Mock();bridge=Mock()
    api.allocate.return_value={"stepId":"fixed-step","argsDigest":"sha256:fixed"}
    error=httpx.ReadTimeout("private transport error")
    bridge.call.side_effect=error;api.request.side_effect=RuntimeError(failure)
    with pytest.raises(httpx.ReadTimeout):tool(api,bridge)._invoke({},None)
    bridge.call.assert_called_once();api.allocate.assert_called_once()
