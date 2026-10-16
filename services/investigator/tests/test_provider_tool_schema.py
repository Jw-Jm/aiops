import copy
import json
from pathlib import Path
from investigator.holmes_adapter import PlatformTool


def test_all_legacy_inputs_exclude_output_contracts_without_mutating_catalog():
    paths = list((Path(__file__).resolve().parents[3] / "api/mcp/tools").glob("*-v2.schema.json"))
    assert len(paths) == 16
    for path in paths:
        schema = json.loads(path.read_text())
        original = copy.deepcopy(schema)
        tool = PlatformTool(name=path.name.split("-v2")[0], description="Read-only tool")
        tool._schema = schema
        parameters = tool.get_openai_format()["function"]["parameters"]
        assert '"$ref"' not in json.dumps(parameters)
        assert parameters["properties"] == original["properties"]
        assert parameters.get("required") == original.get("required")
        assert parameters["additionalProperties"] is False
        assert schema == original


def test_local_input_definitions_remain_available():
    tool = PlatformTool(name="example", description="Read-only tool")
    tool._schema = {"type": "object", "$defs": {"output": {"type": "string"}},
                    "properties": {"value": {"$ref": "#/$defs/output"}}}
    assert tool.get_openai_format()["function"]["parameters"] == tool._schema

def test_data_query_does_not_offer_resource_override_parameters():
    schema={"type":"object","additionalProperties":False,"required":["intent"],"properties":{"intent":{"type":"object"},"canonicalId":{"type":"string"},"windowRole":{"type":"string"}}}
    tool=PlatformTool(name="execute_analysis_query",description="Bound query")
    tool._schema=schema;tool._task_kind="data_query"
    assert set(tool.get_openai_format()["function"]["parameters"]["properties"])=={"intent"}
    assert "canonicalId" in schema["properties"]
    tool._task_kind="resource_troubleshooting"
    assert tool.get_openai_format()["function"]["parameters"]["properties"]==schema["properties"]
