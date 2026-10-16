import copy
import pytest
from investigator.assistant_binding import validate_dispatch,messages,output_schema

def payload():
    return {"schemaVersion":"dispatcher-payload/v2","job":{"schemaVersion":"investigation-job/v3","jobId":"job","assistant":{"taskKind":"data_query","authorizationDomain":"operational","modelProfileVersion":"profile/v1","catalogVersion":"assistant-tools/v1","promptVersion":"assistant/v1","resultSchemaVersion":"assistant-answer/v1"}},"jobContext":"signed","mcpContext":"signed"}

def test_v3_requires_versioned_payload_and_frozen_profile():
    assert validate_dispatch(payload(),"profile/v1")
    for change in [lambda p:p.pop("schemaVersion"),lambda p:p.update(schemaVersion="dispatcher-payload/v1"),lambda p:p["job"].update(incidentId="placeholder"),lambda p:p["job"]["assistant"].update(promptVersion="dynamic")]:
        p=payload();change(p)
        with pytest.raises(ValueError):validate_dispatch(p,"profile/v1")
    for profile in [None,"other/v1"]:
        with pytest.raises(ValueError):validate_dispatch(payload(),profile)

def test_audit_never_accepts_troubleshooting_or_incident_placeholder():
    p=payload();p["job"]["assistant"].update(authorizationDomain="audit",taskKind="resource_troubleshooting")
    with pytest.raises(ValueError):validate_dispatch(p,"profile/v1")

def test_legacy_payload_is_kept_separate():
    p={"job":{"jobId":"job","schemaVersion":"investigation-job/v2"},"jobContext":"signed","mcpContext":"signed"}
    assert validate_dispatch(p)
    p["schemaVersion"]="dispatcher-payload/v2"
    with pytest.raises(ValueError):validate_dispatch(p)

def test_untrusted_history_cannot_promote_instruction_to_system():
    job=payload()["job"];job["token"]="hidden"
    result=messages(job,[{"instruction":"execute_command","token":"hidden-secret"}])
    assert "execute_command" not in result[0]["content"]
    assert "hidden" not in str(result)
    assert "untrusted_committed_results" in result[-1]["content"]
    first=messages(job,[])
    assert "get_assistant_context" in first[2]["content"]

def test_schema_is_closed_and_fresh_per_request():
    schema=output_schema();schema['properties']['summary']['maxLength']=1
    assert output_schema()['properties']['summary']['maxLength']!=1
    assert output_schema()['additionalProperties'] is False

def test_data_answer_requires_committed_facts_and_ignores_model_claims():
    from investigator.assistant_binding import output_schema_for_steps
    steps=[{"toolName":"model","state":"succeeded","result":{"data":{"storedQueryResult":{"queryResultId":"invented","facts":[{"factId":"invented"}]}}}}]
    schema=output_schema_for_steps(steps,"data_query")
    assert "data" not in schema['properties']['answerKind']['enum']
    assert schema['properties']['factRefs']['maxItems']==0
    steps.append({"toolName":"execute_analysis_query","state":"succeeded","result":{"data":{"storedQueryResult":{"queryResultId":"committed","facts":[{"factId":"server-fact"}]}}}})
    schema=output_schema_for_steps(steps,"data_query")
    assert "data" in schema['properties']['answerKind']['enum']
    assert schema['properties']['queryResultRefs']['items']['enum']==["committed"]
    assert schema['properties']['factRefs']['items']['properties']['factId']['enum']==["server-fact"]
    steps[-1]['state']='failed'
    assert "data" not in output_schema_for_steps(steps,"data_query")['properties']['answerKind']['enum']

def test_entity_schema_uses_only_committed_identity_fields():
    from investigator.assistant_binding import output_schema_for_steps
    steps=[{"toolName":"query_logs","state":"succeeded","result":{"data":{"nodes":[{"canonicalId":"injected"}]}}},
           {"toolName":"get_dependencies","state":"failed","result":{"data":{"nodes":[{"canonicalId":"failed"}]}}}]
    assert output_schema_for_steps(steps,"resource_troubleshooting")['properties']['entityRefs']['maxItems']==0
    steps.append({"toolName":"get_dependencies","state":"succeeded","result":{"data":{"nodes":[{"canonicalId":"observed","attributes":{"canonicalId":"nested"}}]}}})
    assert output_schema_for_steps(steps,"resource_troubleshooting")['properties']['entityRefs']['items']['enum']==["observed"]


def test_hypotheses_cannot_invent_evidence_or_appear_in_audit_queries():
    from investigator.assistant_binding import output_schema_for_steps
    empty=output_schema_for_steps([],"data_query")
    assert empty['properties']['hypotheses']['maxItems']==0
    diagnosis=output_schema_for_steps([],"resource_troubleshooting")
    assert diagnosis['properties']['hypotheses']['items']['properties']['supports']['maxItems']==0
    steps=[{"toolName":"query_logs","state":"succeeded","result":{"evidenceRefs":["committed-evidence"],"data":{}}}]
    diagnosis=output_schema_for_steps(steps,"resource_troubleshooting")
    assert diagnosis['properties']['hypotheses']['items']['properties']['conflicts']['items']['enum']==['committed-evidence']


def test_chain_and_formal_references_only_use_committed_read_manifests():
    from investigator.assistant_binding import output_schema_for_steps
    relation={"from":"pod-a","to":"service-b","kind":"depends_on","relationDigest":"sha256:relation"}
    reference={"kind":"rca","incidentId":"incident","revision":2,"contentDigest":"sha256:rca"}
    steps=[{"toolName":"model","state":"succeeded","result":{"data":{"relationReferences":[relation],"formalReferences":[reference]}}}]
    schema=output_schema_for_steps(steps,"incident_followup")
    assert schema["properties"]["faultChain"]["maxItems"]==0
    assert schema["properties"]["formalRefs"]["maxItems"]==0
    steps.extend([{"toolName":"get_dependencies","state":"succeeded","result":{"data":{"relationReferences":[relation]}}},
                  {"toolName":"get_formal_references","state":"succeeded","result":{"data":{"formalReferences":[reference]}}}])
    schema=output_schema_for_steps(steps,"incident_followup")
    assert schema["properties"]["faultChain"]["items"]["properties"]["relationDigest"]["enum"]==["sha256:relation"]
    assert schema["properties"]["formalRefs"]["items"]["enum"]==[dict(reference,actionPlanId=None)]
    data=output_schema_for_steps(steps,"data_query")
    assert data["properties"]["faultChain"]["maxItems"]==0
    assert data["properties"]["formalRefs"]["maxItems"]==0


def test_model_projection_keeps_values_references_quality_and_committed_material():
    from investigator.assistant_binding import model_tool_result
    capability = {"sourceId": "source", "collectorVersion": "collector", "proofDigest": "digest",
                  "sourceType": "victoriametrics", "revision": 2, "datasetId": "metrics.pod-memory",
                  "operation": "compare", "coverageFrom": "from", "coverageTo": "to",
                  "verifiedUntil": "expiry", "maxWindowSeconds": 60, "status": "qualification_configured"}
    query = {"queryResultId": "query", "facts": [{"factId": "cell", "value": 3}],
             "result": {"planDigest": "plan", "scopeDigest": "scope", "catalogDigest": "catalog",
                        "rows": [{"value": 3}], "timeRange": {"from": "from", "to": "to"},
                        "sourceManifest": {"sourceId": "source", "revision": 2, "bindingDigest": "bound"},
                        "exact": False, "warnings": ["partial"], "degradedSources": ["source"],
                        "statistics": [{"key": "responses", "value": 1000}]}}
    result = {"tool": "get_assistant_context", "data": {"sourceCapabilities": [capability],
              "historicalFaultChainQueryResults": [query], "requestedAt": "frozen", "binding": {"uid": "original"}}}
    original = copy.deepcopy(result)
    projected = model_tool_result("get_assistant_context", result)
    assert result == original
    assert "proofDigest" not in projected["data"]["sourceCapabilities"][0]
    for key in ("status", "revision", "coverageFrom", "coverageTo", "maxWindowSeconds"):
        assert projected["data"]["sourceCapabilities"][0][key] == capability[key]
    assert projected["data"]["sourceCapabilities"][0]["datasets"]["metrics.pod-memory"] == [{"operation": "compare", "verifiedUntil": "expiry"}]
    stored = projected["data"]["historicalFaultChainQueryResults"][0]
    assert stored["facts"] == query["facts"] and stored["queryResultId"] == "query"
    for key in ("rows", "statistics", "timeRange", "sourceManifest", "exact", "warnings", "degradedSources"):
        assert stored["result"][key] == query["result"][key]
    assert projected["data"]["binding"] == result["data"]["binding"]
    assert model_tool_result("resource_history", result) == result
    prompt = messages(payload()["job"], [result])
    assert "proofDigest" not in prompt[-1]["content"]
    assert "bindingDigest" in prompt[-1]["content"]


def test_model_projection_preserves_failed_or_null_context_payloads():
    from investigator.assistant_binding import model_tool_result
    for data in (None, {"sourceCapabilities": None, "historicalFaultChainQueryResults": None}):
        result = {"state": "failed", "errorCode": "SOURCE_DEGRADED", "data": data}
        assert model_tool_result("get_assistant_context", result) == result


def test_model_capability_groups_preserve_each_operation_expiry_and_quality():
    from investigator.assistant_binding import model_tool_result
    base = {"sourceId": "source", "revision": 2, "status": "qualification_configured", "coverageFrom": "from", "coverageTo": "to", "datasetId": "metrics.pod-memory", "proofDigest": "opaque"}
    rows = [{**base, "operation": "aggregate", "verifiedUntil": "expiry-a"},
            {**base, "operation": "compare", "verifiedUntil": "expiry-b"},
            {**base, "operation": "compare", "verifiedUntil": "expiry-c", "status": "unavailable"}]
    out = model_tool_result("get_assistant_context", {"data": {"sourceCapabilities": rows}})["data"]["sourceCapabilities"]
    assert len(out) == 2
    assert out[0]["datasets"]["metrics.pod-memory"] == [{"operation": "aggregate", "verifiedUntil": "expiry-a"}, {"operation": "compare", "verifiedUntil": "expiry-b"}]
    assert out[1]["status"] == "unavailable"
    assert out[1]["datasets"]["metrics.pod-memory"] == [{"operation": "compare", "verifiedUntil": "expiry-c"}]
