#!/usr/bin/env python3
"""Project the versioned manual-query schema into the public OpenAPI types."""
import json
from pathlib import Path

root = Path(__file__).resolve().parents[1]
source = json.loads((root / "api/schemas/ui-v12-observability-v1.schema.json").read_text())
common = json.loads((root / "api/schemas/common.schema.json").read_text())
path = root / "api/openapi/platform-v1.yaml"
text = path.read_text()
start, end = "# observation-generated-components-start\n", "# observation-generated-components-end\n"
if start in text:
    text = text[:text.index(start)] + text[text.index(end) + len(end):]

def name(key):
    return "Observation" + key[0].upper() + key[1:] + "V1"

def normalize(value):
    if isinstance(value, list):
        return [normalize(v) for v in value]
    if not isinstance(value, dict):
        return value
    if "$ref" in value:
        ref = value["$ref"]
        if ref.startswith("https://ops.local/schemas/common/v1#/$defs/"):
            return normalize(common["$defs"][ref.split("/")[-1]])
        if ref.startswith("#/$defs/"):
            return {"$ref": "#/components/schemas/" + name(ref.split("/")[-1])}
    if "anyOf" in value and any(v.get("type") == "null" for v in value["anyOf"]):
        alternatives = [v for v in value["anyOf"] if v.get("type") != "null"]
        projected = normalize(alternatives[0]) if len(alternatives) == 1 else {"anyOf": normalize(alternatives)}
        if "$ref" in projected:
            projected = {"allOf": [projected]}
        return {**projected, "nullable": True}
    # Conditional assertions remain in the authoritative JSON Schema checked by
    # the handler; The current OpenAPI generators cannot express those assertions.
    out = {k: normalize(v) for k, v in value.items() if k not in ("if", "then", "else", "not", "$schema", "$id")}
    if "const" in out:
        const = out.pop("const")
        if const is None:
            out.update({"nullable": True})
        else:
            out["enum"] = [const]
    if out.get("type") == "null":
        out.pop("type")
        out["nullable"] = True
    if "allOf" in out:
        out["allOf"] = [v for v in out["allOf"] if v]
        if not out["allOf"]:
            out.pop("allOf")
    return out

components = {name(key): normalize(value) for key, value in source["$defs"].items()}
for key in ("queryView", "resultView", "createRequest", "cancelRequest", "cancelResult"):
    components[name(key)]["x-validation-schema"] = source["$id"] + "#/$defs/" + key
for key in ("queryView", "resultView", "cancelResult"):
    components[name(key) + "Envelope"] = {"type": "object", "required": ["data", "requestId"], "properties": {"data": {"$ref": "#/components/schemas/" + name(key)}, "requestId": {"type": "string"}}}
components["ObservationCapabilitiesV1"] = {"type":"object","required":["schemaVersion","resources","sources","templates","budget"],"properties":{
 "schemaVersion":{"type":"string","enum":["ui-v12-observability-capabilities/v1"]},
 "resources":{"type":"array","items":{**normalize(source["$defs"]["resourceIdentity"]),"properties":{**normalize(source["$defs"]["resourceIdentity"])["properties"],"displayName":{"type":"string"}}}},
 "sources":{"type":"array","items":{"type":"object","required":["name","source","coverageFrom","coverageTo","verifiedUntil","maxWindowSeconds"],"properties":{"name":{"type":"string"},"source":{"$ref":"#/components/schemas/ObservationSourceBindingV1"},**{key:{"type":"string","format":"date-time"} for key in ["coverageFrom","coverageTo","verifiedUntil"]},"maxWindowSeconds":{"type":"integer"}}}},
 "templates":{"type":"array","items":{"type":"string"}},"budget":{"$ref":"#/components/schemas/ObservationBudgetV1"}}}
components["ObservationCapabilitiesEnvelopeV1"] = {"type":"object","required":["data","requestId"],"properties":{"data":{"$ref":"#/components/schemas/ObservationCapabilitiesV1"},"requestId":{"type":"string"}}}
components["ObservationHistoryEnvelopeV1"] = {"type":"object","required":["data","requestId","nextCursor"],"properties":{"nextCursor":{"type":"string","format":"uuid","nullable":True},"data":{"type":"array","items":{"$ref":"#/components/schemas/ObservationQueryViewV1"}},"requestId":{"type":"string"}}}
blob = start + "".join("    " + key + ": " + json.dumps(value, ensure_ascii=False) + "\n" for key, value in components.items()) + end
text = text.replace("  schemas:\n", "  schemas:\n" + blob, 1)
paths = {}
for suffix, method, operation, result, body, status in [
 ("capabilities","get","getObservationCapabilities","ObservationCapabilitiesEnvelopeV1",None,"200"),
 ("queries","get","listObservationQueries","ObservationHistoryEnvelopeV1",None,"200"),
 ("queries","post","createObservationQuery","ObservationQueryViewV1Envelope","ObservationCreateRequestV1","202"),
 ("queries/{queryId}","get","getObservationQuery","ObservationQueryViewV1Envelope",None,"200"),
 ("queries/{queryId}/results","get","getObservationQueryResults","ObservationResultViewV1Envelope",None,"200"),
 ("queries/{queryId}:cancel","post","cancelObservationQuery","ObservationCancelResultV1Envelope","ObservationCancelRequestV1","200")]:
 params=[{"in":"query","name":"clusterUid","required":True,"schema":{"type":"string"}}]
 if "{queryId}" in suffix:params.append({"in":"path","name":"queryId","required":True,"schema":{"type":"string","format":"uuid"}})
 if operation=="listObservationQueries":params.extend([{ "in":"query","name":"cursor","schema":{"type":"string","format":"uuid"}}, {"in":"query","name":"limit","schema":{"type":"integer","minimum":1,"maximum":50,"default":20}}])
 if suffix=="capabilities":params.append({"in":"query","name":"canonicalId","allowReserved":False,"schema":{"type":"string","format":"canonical-id"}})
 if operation=="createObservationQuery":params.append({"in":"header","name":"Idempotency-Key","required":True,"schema":{"type":"string","format":"uuid"}})
 item={"operationId":operation,"security":[{"NativeSessionCookie":[]}],"parameters":params,"responses":{status:{"description":"Current authorized manual query response; never cached.","content":{"application/json":{"schema":{"$ref":"#/components/schemas/"+result}}}},"default":{"description":"Unified error","content":{"application/json":{"schema":{"$ref":"#/components/schemas/ErrorEnvelope"}}}}}}
 if body:item["requestBody"]={"required":True,"content":{"application/json":{"schema":{"$ref":"#/components/schemas/"+body}}}};item["x-idempotency"]="required" if operation=="createObservationQuery" else "non-idempotent"
 paths.setdefault("/api/v1/observability/"+suffix,{})[method]=item
pstart,pend="# observation-generated-paths-start\n","# observation-generated-paths-end\n"
if pstart in text:
 text=text[:text.index(pstart)]+text[text.index(pend)+len(pend):]
elif "  /api/v1/observability/capabilities:\n" in text:
 a=text.index("  /api/v1/observability/capabilities:\n");z=text.index("# assistant-generated-paths-start",a);text=text[:a]+text[z:]
text=text.replace("paths:\n","paths:\n"+pstart+"".join("  "+key+": "+json.dumps(value,ensure_ascii=False)+"\n" for key,value in paths.items())+pend,1)
path.write_text(text)
