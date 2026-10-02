package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	api "ops-platform/gen/api"
	"testing"
	"time"
)

func TestDiagnosticInputConsumesGeneratedContractAndDeadline(t *testing.T) {
	b, _ := json.Marshal(api.DiagnosticGraphBuildRequest{EntryCanonicalId: "k8s+v1://tenant-a/cluster-a/core/Pod/pod-a", Recipe: api.Incident, Policy: api.GraphBudget{MaxDepth: 2, MaxNodes: 200, MaxEdges: 400, TimeoutMs: 3000}})
	r := httptest.NewRequest("POST", "/api/v1/diagnostic-graphs:build", bytes.NewReader(b))
	q, deadline, err := diagnosticInput(r)
	if err != nil || q.MaxDepth != 2 || deadline != 3*time.Second {
		t.Fatalf("generated contract not consumed: %+v %v %v", q, deadline, err)
	}
	for _, payload := range []string{`{"entryCanonicalId":"x","recipe":"incident","policy":{"maxDepth":2,"maxNodes":200,"maxEdges":400}}`, `{"entryCanonicalId":"x","recipe":"incident","policy":{"maxDepth":2,"maxNodes":200,"maxEdges":400,"timeoutMs":30001}}`} {
		if _, _, err := diagnosticInput(httptest.NewRequest("POST", "/", bytes.NewBufferString(payload))); err == nil {
			t.Fatal("invalid deadline accepted")
		}
	}
}

func TestNeighborInputBindsDeclaredParameters(t *testing.T) {
	r := httptest.NewRequest("GET", "/?direction=out&relationKind=scheduled_on&depth=2&maxNodes=3&maxEdges=2", nil)
	q, err := neighborInput(r)
	if err != nil || q.Direction != "out" || q.MaxDepth != 2 || q.MaxNodes != 3 || q.MaxEdges != 2 || len(q.RelationKinds) != 1 || q.RelationKinds[0] != "scheduled_on" {
		t.Fatalf("parameters ignored: %+v %v", q, err)
	}
	for _, query := range []string{"direction=bad", "depth=3", "maxNodes=201", "maxEdges=401", "depth=-1", "depth=oops"} {
		if _, err := neighborInput(httptest.NewRequest("GET", "/?"+query, nil)); err == nil {
			t.Fatal(query)
		}
	}
}
