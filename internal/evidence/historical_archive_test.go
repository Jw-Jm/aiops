package evidence

import (
	"github.com/google/uuid"
	"ops-platform/internal/archive"
	"ops-platform/internal/datascope"
	"testing"
)

func TestHistoricalArchiveKeepsProvenanceAndCurrentScopeFences(t *testing.T) {
	e := validCapture()
	e.SourceScopeDigest = Digest([]byte("captured binding"))
	e.ReplayState = "archived_verified"
	e.ArchiveRef = &ArchiveRef{PlaintextDigest: e.ContentDigest, CiphertextDigest: "cipher", EncryptionKeyVersion: "key-v1", Object: archive.ObjectRef{TenantID: uuid.MustParse(e.TenantID), ObjectID: uuid.MustParse(e.EvidenceID), Category: "evidence", VersionID: "version", Digest: "cipher"}}
	current := Binding{Tenant: e.TenantID, SourceID: e.SourceRegistrationID, Revision: 2, SourceType: e.SourceSystem, BackendLogicalID: e.BackendLogicalID, ScopeMapping: datascope.Mapping{Scopes: map[string][]string{"cluster": {"cluster-a"}, "namespace": {"apps"}}}}
	if !historicalArchiveAllowed(e, "apps", current) {
		t.Fatal("verified historical archive denied")
	}
	for name, mutate := range map[string]func(*Evidence, *Binding){
		"unverified":             func(e *Evidence, _ *Binding) { e.ReplayState = "archive_pending" },
		"missing provenance":     func(e *Evidence, _ *Binding) { e.SourceScopeDigest = "" },
		"same revision mismatch": func(_ *Evidence, b *Binding) { b.Revision = 1 },
		"future observation":     func(e *Evidence, _ *Binding) { e.SourceRevision = 3 },
		"backend changed":        func(_ *Evidence, b *Binding) { b.BackendLogicalID = "different" },
		"source changed":         func(_ *Evidence, b *Binding) { b.SourceID = uuid.NewString() },
		"tenant changed":         func(_ *Evidence, b *Binding) { b.Tenant = uuid.NewString() },
		"cluster removed":        func(_ *Evidence, b *Binding) { b.ScopeMapping = datascope.Mapping{} },
		"namespace removed": func(_ *Evidence, b *Binding) {
			b.ScopeMapping = datascope.Mapping{Scopes: map[string][]string{"cluster": {"cluster-a"}}}
		},
		"archive replaced": func(e *Evidence, _ *Binding) {
			ref := *e.ArchiveRef
			ref.Object.ObjectID = uuid.New()
			e.ArchiveRef = &ref
		},
		"digest changed": func(e *Evidence, _ *Binding) { e.ContentDigest = Digest([]byte("changed")) },
	} {
		t.Run(name, func(t *testing.T) {
			bad, binding := e, current
			mutate(&bad, &binding)
			if historicalArchiveAllowed(bad, "apps", binding) {
				t.Fatal("invalid historical read allowed")
			}
		})
	}
}
