package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"ops-platform/internal/investigation"
)

// The public step result must keep the public business facts of a tool result
// and drop the internal admission material of the assistant context. The
// projection never rewrites the stored bytes.
func TestPublicStepResultKeepsPublicFactsAndDropsInternalContext(t *testing.T) {
	stored := json.RawMessage(`{
		"schemaVersion":"tool-response/v3",
		"tool":"get_assistant_context",
		"state":"succeeded",
		"partial":false,
		"evidenceRefs":["ev-1","ev-2"],
		"degradedSources":["s1"],
		"budget":{"consumed":{"toolCalls":1}},
		"data":{
			"focus":"查询节点故障",
			"schemaVersion":"assistant-input/v1",
			"requestedAt":"2026-11-10T00:00:00Z",
			"binding":{"timezone":"UTC","grantSnapshot":{"tenantId":"t1","clusterUid":"c1"},"executionScope":{"tenantId":"t1"},"contextDigest":"sha256:abc"},
			"history":[{"role":"user","content":"内部历史"}],
			"historyManifestDigest":"sha256:def",
			"message":"内部提示词",
			"analysisCatalog":{"datasets":[{"id":"d1"}]},
			"effectiveBudget":{"toolCalls":8},
			"sourceCapabilities":[{"sourceId":"s1","proofDigest":"sha256:1"}],
			"networkCapabilities":[{"sourceId":"s2"}],
			"sourceCapabilityStatus":"available"
		}
	}`)
	projected := publicStepResult("get_assistant_context", stored)
	body := string(projected)
	for _, leaked := range []string{
		"grantSnapshot", "grant_snapshot", "contextDigest", "context_digest",
		"history", "historyManifestDigest", "message", "analysisCatalog",
		"effectiveBudget", "sourceCapabilities", "networkCapabilities",
		"sourceCapabilityStatus", "binding", "executionScope",
	} {
		if strings.Contains(body, leaked) {
			t.Fatalf("the public assistant context publishes %q: %s", leaked, body)
		}
	}
	for _, want := range []string{`"focus":"查询节点故障"`, `"schemaVersion":"assistant-input/v1"`, `"evidenceRefs":["ev-1","ev-2"]`, `"degradedSources":["s1"]`, `"consumed"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("the public assistant context misses %s: %s", want, body)
		}
	}
	if !strings.Contains(string(stored), "内部历史") {
		t.Fatal("the stored result was rewritten by the projection")
	}
}

// The fixed RCA revision is a public business fact. Its input digest is the
// provenance of that revision and must survive the projection: a projection
// that deletes every key named input digest is wrong.
func TestPublicStepResultKeepsFormalRCADigest(t *testing.T) {
	stored := json.RawMessage(`{
		"schemaVersion":"tool-response/v3",
		"tool":"get_formal_references",
		"state":"succeeded",
		"evidenceRefs":["ev-3"],
		"data":{
			"formalReferences":[{"kind":"rca","revision":"sha256:rev","contentDigest":"sha256:content"}],
			"rca":{"revision":"sha256:rev","inputDigest":"sha256:rca-input","sourceRevision":7},
			"currentRcaRevision":3,
			"historical":false
		}
	}`)
	body := string(publicStepResult("get_formal_references", stored))
	for _, want := range []string{`"inputDigest":"sha256:rca-input"`, `"contentDigest":"sha256:content"`, `"revision":"sha256:rev"`, `"sourceRevision":7`, `"currentRcaRevision":3`, `"evidenceRefs":["ev-3"]`} {
		if !strings.Contains(body, want) {
			t.Fatalf("the public formal reference misses %s: %s", want, body)
		}
	}
}

// The public step view keeps the identity of the step and the public result,
// and never the internal error surface of the stored row.
func TestPublicInvestigationStepView(t *testing.T) {
	step := investigation.Step{
		StepID:     uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Name:       "get_formal_references",
		ArgsDigest: "sha256:args",
		State:      "succeeded",
		Result:     json.RawMessage(`{"schemaVersion":"tool-response/v3","tool":"get_formal_references","state":"succeeded","data":{"rca":{"inputDigest":"sha256:rca-input"}}}`),
	}
	view := publicInvestigationStepView(step)
	if view.StepID != step.StepID || view.Name != step.Name || view.ArgsDigest != step.ArgsDigest || view.State != step.State {
		t.Fatalf("the public step lost its identity: %+v", view)
	}
	if !strings.Contains(string(view.Result), `"inputDigest":"sha256:rca-input"`) {
		t.Fatalf("the public step lost the provenance of the fixed RCA: %s", view.Result)
	}
	views := publicInvestigationStepViews([]investigation.Step{step})
	if len(views) != 1 {
		t.Fatalf("the public step list = %d", len(views))
	}
}

// The model step keeps the raw provider response in the stored row. The public
// view publishes the provider and the usage state only.
func TestPublicStepResultOfModelStepKeepsNoRawResponse(t *testing.T) {
	stored := json.RawMessage(`{"provider":"openai-compatible","usageKnown":true,"response":{"choices":[{"message":{"content":"内部提示词与授权快照"}}]}}`)
	body := string(publicStepResult("model", stored))
	if strings.Contains(body, "内部提示词与授权快照") || strings.Contains(body, "choices") {
		t.Fatalf("the public model step publishes the raw provider response: %s", body)
	}
	if !strings.Contains(body, `"provider":"openai-compatible"`) || !strings.Contains(body, `"usageKnown":true`) {
		t.Fatalf("the public model step misses the provider facts: %s", body)
	}
}

// The resource investigation context carries the same admission digests as the
// assistant context and keeps only the resolved resources and the related
// incidents.
func TestPublicStepResultOfResourceInvestigationContext(t *testing.T) {
	stored := json.RawMessage(`{
		"schemaVersion":"tool-response/v3",
		"tool":"get_resource_investigation_context",
		"state":"succeeded",
		"data":{
			"context":{"contextId":"c","resources":[{"canonicalId":"k8s://Pod/a"}],"grantSnapshotDigest":"sha256:g","contextDigest":"sha256:c","executionScopeDigest":"sha256:e"},
			"relatedIncidents":[{"incidentId":"i"}],
			"relatedIncidentsTruncated":false
		}
	}`)
	body := string(publicStepResult("get_resource_investigation_context", stored))
	for _, leaked := range []string{"grantSnapshotDigest", "contextDigest", "executionScopeDigest"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("the public resource context publishes %q: %s", leaked, body)
		}
	}
	for _, want := range []string{`"canonicalId":"k8s://Pod/a"`, `"relatedIncidents":[{"incidentId":"i"}]`} {
		if !strings.Contains(body, want) {
			t.Fatalf("the public resource context misses %s: %s", want, body)
		}
	}
}

// A legacy step of a v2 job keeps its public result: the compatibility of the
// old contract must not depend on the tool.
func TestPublicStepResultOfLegacyStep(t *testing.T) {
	stored := json.RawMessage(`{"schemaVersion":"tool-response/v2","tool":"get_incident_context","state":"succeeded","data":{"incident":{"incidentId":"i"},"findings":[{"id":"f"}]}}`)
	body := string(publicStepResult("get_incident_context", stored))
	for _, want := range []string{`"incident":{"incidentId":"i"}`, `"findings":[{"id":"f"}]`, `"schemaVersion":"tool-response/v2"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("the public legacy step misses %s: %s", want, body)
		}
	}
}

func TestPublicAnalysisStepKeepsFactsWithoutRecoveryScope(t *testing.T) {
	raw := json.RawMessage(`{"tool":"execute_analysis_query","state":"succeeded","data":{"scope":{"authorizationRevision":"private-revision","namespaces":["apps"]},"queryResultRefs":["result-1"],"filters":[{"field":"kind","operator":"eq","value":"Pod"}],"storedQueryResult":{"result":{"rows":[{"dimension":"normal","value":7}]}}}}`)
	body := string(publicStepResult("execute_analysis_query", raw))
	if strings.Contains(body, "authorizationRevision") || strings.Contains(body, `"scope"`) {
		t.Fatal("recovery authority published", body)
	}
	for _, want := range []string{`"queryResultRefs":["result-1"]`, `"value":7`, `"field":"kind"`} {
		if !strings.Contains(body, want) {
			t.Fatal("public fact lost", body)
		}
	}
	if !strings.Contains(string(raw), "private-revision") {
		t.Fatal("stored input changed")
	}
}
