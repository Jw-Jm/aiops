package npd

import (
	"encoding/json"
	"ops-platform/internal/evidence"
	"ops-platform/internal/resource"
	"testing"
	"time"
)

func TestNativeKernelTransportIsRequiredAndNotInferredFromText(t *testing.T) {
	clock := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	for _, transport := range []string{"kernel", "stdout", "journal", ""} {
		t.Run(transport, func(t *testing.T) {
			data, _ := json.Marshal([]map[string]string{{"_time": clock.Format(time.RFC3339Nano), "_msg": "BUG: unable to handle kernel NULL pointer dereference at 0000", "_TRANSPORT": transport}})
			e := evidence.Evidence{ResourceCanonicalID: (resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Node", StableID: "node-a"}).String(), SourceSystem: "victorialogs", QueryTemplateVersion: "node-kernel-logs/v1", TimeReliable: true, Data: data, ContentDigest: evidence.Digest(data), ObservedFrom: clock.Add(-time.Minute), ObservedTo: clock}
			candidates, err := NativeKernelCandidates(e)
			if transport == "kernel" {
				if err != nil || len(candidates) != 1 {
					t.Fatalf("trusted kernel record rejected: %v %+v", err, candidates)
				}
			} else if err == nil || len(candidates) != 0 {
				t.Fatalf("application/missing transport became native kernel proof: %v %+v", err, candidates)
			}
		})
	}
}
