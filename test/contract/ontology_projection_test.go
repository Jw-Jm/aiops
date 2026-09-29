package contract

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	platformcontract "ops-platform/internal/contract"
)

type ontologyDiagnosticFixture struct {
	Entry struct {
		Kind        string `json:"kind"`
		CanonicalID string `json:"canonicalId"`
		Namespace   string `json:"namespace"`
		Name        string `json:"name"`
	} `json:"entry"`
	Nodes []struct {
		CanonicalID string         `json:"canonicalId"`
		Kind        string         `json:"kind"`
		SourceKind  string         `json:"sourceKind"`
		Name        string         `json:"name"`
		Namespace   string         `json:"namespace"`
		Attributes  map[string]any `json:"attributes"`
	} `json:"nodes"`
	Edges []struct {
		From       string `json:"from"`
		To         string `json:"to"`
		Kind       string `json:"kind"`
		Provenance struct {
			SourceType string     `json:"sourceType"`
			State      string     `json:"state"`
			Resolver   string     `json:"resolver"`
			LastSeenAt *time.Time `json:"lastSeenAt"`
			Confidence *float64   `json:"confidence"`
		} `json:"provenance"`
	} `json:"edges"`
	CollectedAt time.Time `json:"collectedAt"`
	Partial     bool      `json:"partial"`
	Warnings    []struct {
		Code string `json:"code"`
	} `json:"warnings"`
	DegradedSources []struct {
		Source string `json:"source"`
		Status string `json:"status"`
	} `json:"degradedSources"`
	Budgets struct {
		MaxDepth int `json:"maxDepth"`
		MaxNodes int `json:"maxNodes"`
		MaxEdges int `json:"maxEdges"`
	} `json:"budgets"`
}

func TestOntologyDiagnosticProjectsToPlatformContract(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "test", "fixtures", "upstream-graph", "ontology-ariadne-diagnostic.golden.json")
	contents, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read locked ontology diagnostic fixture: %v", err)
	}
	var ontology ontologyDiagnosticFixture
	if err := json.Unmarshal(contents, &ontology); err != nil {
		t.Fatalf("decode ontology diagnostic fixture: %v", err)
	}

	for _, degraded := range []bool{false, true} {
		payload, err := projectOntologyDiagnostic(ontology, degraded)
		if err != nil {
			t.Fatalf("project ontology response (degraded=%t): %v", degraded, err)
		}
		if err := platformcontract.Validate("https://ops.local/schemas/diagnostic-graph/v1", payload); err != nil {
			t.Fatalf("projected diagnostic graph rejected by public Contract (degraded=%t): %v\n%s", degraded, err, payload)
		}
		var projected map[string]any
		if err := json.Unmarshal(payload, &projected); err != nil {
			t.Fatal(err)
		}
		if projected["partial"] != degraded {
			t.Fatalf("partial=%v, want %t", projected["partial"], degraded)
		}
		freshness := projected["freshness"].(map[string]any)["kubernetes"].(map[string]any)
		wantState := "fresh"
		if degraded {
			wantState = "partial"
		}
		if freshness["staleness"] != wantState {
			t.Fatalf("freshness staleness=%v, want %s", freshness["staleness"], wantState)
		}
		if degraded && len(projected["degradedSources"].([]any)) == 0 {
			t.Fatal("degraded projection omitted its failed source")
		}
	}
}

func projectOntologyDiagnostic(source ontologyDiagnosticFixture, degraded bool) ([]byte, error) {
	collectedAt := source.CollectedAt.UTC().Format(time.RFC3339Nano)
	entryNode := struct {
		CanonicalID string
		Kind        string
		Name        string
		Namespace   string
	}{
		CanonicalID: source.Entry.CanonicalID, Kind: source.Entry.Kind, Name: source.Entry.Name, Namespace: source.Entry.Namespace,
	}
	var entities []map[string]any
	entityByID := make(map[string]map[string]any, len(source.Nodes))
	for _, node := range source.Nodes {
		entity := ontologyEntity(node.CanonicalID, node.Kind, node.Name, node.Namespace, node.Attributes, collectedAt)
		entities = append(entities, entity)
		entityByID[node.CanonicalID] = entity
	}
	entry, ok := entityByID[entryNode.CanonicalID]
	if !ok {
		return nil, fmt.Errorf("entry %q is absent from ontology nodes", entryNode.CanonicalID)
	}
	edges := make([]map[string]any, 0, len(source.Edges))
	for _, edge := range source.Edges {
		observedAt := collectedAt
		if edge.Provenance.LastSeenAt != nil {
			observedAt = edge.Provenance.LastSeenAt.UTC().Format(time.RFC3339Nano)
		}
		confidence := 0.0
		if edge.Provenance.Confidence != nil {
			confidence = *edge.Provenance.Confidence
		}
		sourceType := edge.Provenance.SourceType
		if sourceType == "observed" {
			sourceType = "runtime_observed"
		}
		resolver := any(nil)
		if edge.Provenance.Resolver != "" {
			resolver = strings.NewReplacer("/", "-", "_", "-").Replace(edge.Provenance.Resolver)
		}
		edges = append(edges, map[string]any{
			"from": entityByID[edge.From]["canonicalId"], "to": entityByID[edge.To]["canonicalId"],
			"kind":       strings.ReplaceAll(edge.Kind, "_", "-"),
			"provenance": map[string]any{"sourceType": sourceType, "state": edge.Provenance.State, "resolver": resolver},
			"confidence": confidence, "observedAt": observedAt, "validFrom": observedAt, "validTo": nil,
		})
	}
	warnings := make([]string, 0, len(source.Warnings)+1)
	for _, warning := range source.Warnings {
		warnings = append(warnings, warning.Code)
	}
	degradedSources := make([]string, 0, len(source.DegradedSources)+1)
	for _, item := range source.DegradedSources {
		degradedSources = append(degradedSources, item.Source+":"+item.Status)
	}
	freshness := map[string]any{"ready": true, "lastListAt": collectedAt, "lastWatchAt": collectedAt, "watchContinuous": true, "lastError": nil, "staleness": "fresh"}
	partial := source.Partial
	if degraded {
		partial = true
		warnings = append(warnings, "kubernetes-source-unavailable")
		degradedSources = append(degradedSources, "kubernetes")
		freshness = map[string]any{"ready": false, "lastListAt": collectedAt, "lastWatchAt": nil, "watchContinuous": false, "lastError": "Kubernetes list/watch source unavailable", "staleness": "partial"}
	}
	if warnings == nil {
		warnings = []string{}
	}
	if degradedSources == nil {
		degradedSources = []string{}
	}
	maxNodes := source.Budgets.MaxNodes
	if maxNodes < 1 || maxNodes > 1000 {
		maxNodes = 1000
	}
	maxEdges := source.Budgets.MaxEdges
	if maxEdges < 1 || maxEdges > 100000 {
		maxEdges = 100000
	}
	payload := map[string]any{
		"schemaVersion": "diagnostic-graph/v1", "entry": entry, "nodes": entities, "edges": edges,
		"collectedAt": collectedAt, "partial": partial, "warnings": warnings, "degradedSources": degradedSources,
		"budgets":        map[string]any{"maxDepth": source.Budgets.MaxDepth, "maxNodes": maxNodes, "maxEdges": maxEdges, "timeout": 3000},
		"rankedEvidence": []any{}, "conflicts": []any{}, "freshness": map[string]any{"kubernetes": freshness},
	}
	return json.Marshal(payload)
}

func ontologyEntity(canonicalID, kind, name, namespace string, attributes map[string]any, observedAt string) map[string]any {
	parts := strings.Split(canonicalID, "/")
	if len(parts) != 7 {
		return map[string]any{}
	}
	cluster, group, stableID := parts[0], parts[1], parts[5]
	if stableID == "_" || stableID == "" {
		stableID = parts[4]
	}
	domain := "kubernetes"
	if strings.Contains(group, "hardware") {
		domain = "hardware"
	}
	if strings.Contains(group, "deepflow") {
		domain = "deepflow"
	}
	publicID := domain + "+v1://tenant-a/" + cluster + "/" + group + "/" + kind + "/" + stableID
	var publicNamespace any = namespace
	if namespace == "" {
		publicNamespace = nil
	}
	if attributes == nil {
		attributes = map[string]any{}
	}
	return map[string]any{
		"canonicalId": publicID, "tenantId": "tenant-a", "domain": domain, "scope": cluster,
		"apiGroup": group, "kind": kind, "stableId": stableID, "name": name, "namespace": publicNamespace,
		"labels": map[string]any{}, "attributes": attributes, "sourceRefs": []any{},
		"firstObservedAt": observedAt, "lastObservedAt": observedAt,
	}
}
