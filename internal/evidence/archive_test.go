package evidence

import (
	"github.com/google/uuid"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
	"testing"
	"time"
)

func validCapture() Evidence {
	tenant := uuid.NewString()
	id := resource.CanonicalID{Domain: "k8s", Tenant: tenant, Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "pod-a"}.String()
	now := time.Now()
	data := []byte(`{"samples":[]}`)
	return Evidence{SchemaVersion: "evidence/v2", Type: "metric", DataClass: "D1", IndependenceGroup: "test", EvidenceID: uuid.NewString(), TenantID: tenant, ResourceCanonicalID: id, SourceRegistrationID: uuid.NewString(), SourceRevision: 1, SourceSystem: "victoriametrics", BackendLogicalID: "metrics-a", QueryTemplateVersion: "pod-phase/v1", QueryHash: Digest([]byte("query")), EffectiveScope: graph.Scope{Tenant: tenant, Cluster: "cluster-a", Namespaces: []string{"apps"}, AuthorizationRevision: "revision-a"}, EvaluatedAt: now, ObservedFrom: now.Add(-time.Minute), ObservedTo: now, SourceRetentionUntil: now.Add(time.Hour), ContentDigest: Digest(data), Data: data}
}
func TestCaptureRequiresScopedImmutableProvenance(t *testing.T) {
	e := validCapture()
	retain := e.EvaluatedAt.Add(180 * 24 * time.Hour)
	if err := validateCapture(e, "apps", retain); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Evidence){
		"foreign-tenant":      func(e *Evidence) { e.TenantID = uuid.NewString() },
		"foreign-cluster":     func(e *Evidence) { e.EffectiveScope.Cluster = "cluster-b" },
		"empty-authorization": func(e *Evidence) { e.EffectiveScope.AuthorizationRevision = "" },
		"missing-template":    func(e *Evidence) { e.QueryTemplateVersion = "" },
		"digest":              func(e *Evidence) { e.Data = []byte("changed") },
		"source-revision":     func(e *Evidence) { e.SourceRevision = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			bad := e
			mutate(&bad)
			if validateCapture(bad, "apps", retain) == nil {
				t.Fatal("invalid capture accepted")
			}
		})
	}
	if validateCapture(e, "other", retain) == nil {
		t.Fatal("foreign namespace accepted")
	}
}

func TestCaptureRejectsMissingDataClassAndProvenanceScopeRebind(t *testing.T) {
	e := validCapture()
	e.Type = "metric"
	e.DataClass = "D1"
	e.IndependenceGroup = e.SourceRegistrationID
	e.DerivationEvidenceRefs = []string{}
	e.DataClass = "operational"
	if err := validateCapture(e, "apps", e.EvaluatedAt.Add(180*24*time.Hour)); err == nil {
		t.Fatal("non-contract data class accepted")
	}
}
