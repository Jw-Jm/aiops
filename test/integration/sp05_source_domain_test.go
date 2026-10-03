package integration

import (
	"errors"
	"ops-platform/internal/finding"
	"ops-platform/internal/resource"
	"testing"
)

func TestSP05KubernetesBindingCannotForgeHardwareDomain(t *testing.T) {
	ctx, db, pool, b := sp05Database(t)
	e := sp05Envelope(b, "forged-domain", "forged-occurrence")
	e.ResourceCanonicalID = resource.CanonicalID{Domain: "hardware", Tenant: b.TenantID.String(), Scope: b.ClusterUID, APIGroup: "redfish", Kind: "DIMM", StableID: "server/DIMM1"}.String()
	if _, _, err := (finding.Service{Pool: pool}).Ingest(ctx, b, e); !errors.Is(err, finding.ErrUnauthorized) {
		t.Fatalf("kubernetes source forged a hardware Finding: %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM finding.records WHERE tenant_id=$1`, b.TenantID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("domain rejection partially mutated: count=%d err=%v", count, err)
	}
}
