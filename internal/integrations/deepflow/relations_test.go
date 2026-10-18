package deepflow

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"ops-platform/internal/evidence"
)

func TestNetworkRelationsSurviveEvidenceTransportWithoutRenewingTime(t *testing.T) {
	observed := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	rows, _ := json.Marshal([]map[string]any{{"from": "k8s+v1://tenant/cluster/core/Pod/a", "to": "k8s+v1://tenant/cluster/core/Pod/b", "observedAt": observed}})
	original := evidence.Result{Evidence: []evidence.Evidence{{Data: rows, FactSlice: rows, SourceRegistrationID: "source", QueryTemplateVersion: "GetNetworkDependencies"}}}
	want, err := NetworkRelations(original)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(original)
	var transported evidence.Result
	if json.Unmarshal(raw, &transported) != nil || len(transported.Evidence[0].Data) != 0 {
		t.Fatal("not an HTTP transport round trip")
	}
	got, err := NetworkRelations(transported)
	if err != nil || len(got) != 1 || !reflect.DeepEqual(got[0], want[0]) || !got[0].ObservedAt.Equal(observed) || got[0].TTLSeconds != 300 {
		t.Fatal("transport lost relation or renewed historical observation", got, err)
	}
	transported.Evidence[0].FactSlice = json.RawMessage(`[{"resourceCanonicalId":"k8s+v1://tenant/cluster/core/Pod/a","peerScopeLimited":true}]`)
	if got, err := NetworkRelations(transported); err != nil || len(got) != 0 {
		t.Fatal("transport disclosed limited peer", got, err)
	}
}
