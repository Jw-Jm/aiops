package investigation

import (
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
	"testing"
)

func TestArchivedGraphSourceRequiresVerifiedCaptureAndCurrentNamespace(t *testing.T) {
	source := graph.SourceAuthority{SourceRegistrationID: "source", Revision: 1, ScopeDigest: "frozen"}
	current := evidence.Binding{Tenant: "tenant", SourceID: "source", SourceType: "kubernetes", BackendLogicalID: "native", Revision: 2, ScopeMapping: datascope.Mapping{Scopes: map[string][]string{"cluster": {"cluster"}, "namespace": {"apps"}}}}
	proof := evidence.Evidence{TenantID: "tenant", SourceRegistrationID: "source", SourceSystem: "kubernetes", BackendLogicalID: "native", SourceRevision: 1, SourceScopeDigest: "frozen", ReplayState: "archived_verified", ArchiveRef: &evidence.ArchiveRef{}}
	// Canonical IDs require UUID tenant/UID fields.
	current.Tenant = "11111111-1111-4111-8111-111111111111"
	proof.TenantID = current.Tenant
	result := graph.Result{Nodes: []resource.Entity{{CanonicalID: "k8s+v1://" + current.Tenant + "/cluster/core/Pod/22222222-2222-4222-8222-222222222222", Namespace: "apps", SourceRefs: []string{"source"}}}}
	if !archivedGraphSourceAllowed(source, current, result, []evidence.Evidence{proof}) {
		t.Fatal("authorized historical capture denied")
	}
	for _, tc := range []struct {
		name   string
		change func(*evidence.Binding, *evidence.Evidence)
	}{
		{"backend", func(b *evidence.Binding, _ *evidence.Evidence) { b.BackendLogicalID = "other" }},
		{"namespace revoked", func(b *evidence.Binding, _ *evidence.Evidence) {
			b.ScopeMapping = datascope.Mapping{Scopes: map[string][]string{"cluster": {"cluster"}}}
		}},
		{"future", func(_ *evidence.Binding, e *evidence.Evidence) { e.SourceRevision = 3 }},
		{"unverified", func(_ *evidence.Binding, e *evidence.Evidence) { e.ReplayState = "archive_pending" }},
		{"digest", func(_ *evidence.Binding, e *evidence.Evidence) { e.SourceScopeDigest = "changed" }},
		{"archive absent", func(_ *evidence.Binding, e *evidence.Evidence) { e.ArchiveRef = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, e := current, proof
			tc.change(&b, &e)
			if archivedGraphSourceAllowed(source, b, result, []evidence.Evidence{e}) {
				t.Fatal("invalid provenance allowed")
			}
		})
	}
}
