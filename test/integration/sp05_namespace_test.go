package integration

import (
	"errors"
	"ops-platform/internal/finding"
	"ops-platform/internal/resource"
	"testing"
)

func TestSP05ResourceNamespaceCannotBeClaimedAsClusterScoped(t *testing.T) {
	ctx, db, pool, b := sp05Database(t)
	e := sp05Envelope(b, "forged-namespace", "o1")
	e.ResourceCanonicalID = resource.CanonicalID{Domain: "k8s", Tenant: b.TenantID.String(), Scope: b.ClusterUID, APIGroup: "core", Kind: "Pod", StableID: "real-pod"}.String()
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.resource_entities(tenant_id,canonical_id,cluster_id,kind,namespace,name,metadata,observed_at) VALUES($1,$2,$3,'Pod','restricted','pod','{}',clock_timestamp())`, b.TenantID, e.ResourceCanonicalID, b.ClusterID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := (finding.Service{Pool: pool}).Ingest(ctx, b, e); !errors.Is(err, finding.ErrUnauthorized) {
		t.Fatalf("claimed cluster namespace accepted: %v", err)
	}
	e.Namespace = "apps"
	if _, _, err := (finding.Service{Pool: pool}).Ingest(ctx, b, e); !errors.Is(err, finding.ErrUnauthorized) {
		t.Fatalf("known actual namespace ignored: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM finding.records`).Scan(&n); err != nil || n != 0 {
		t.Fatal("partial scope mutation")
	}
}
