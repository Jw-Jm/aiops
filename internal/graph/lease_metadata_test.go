package graph

import (
	"encoding/json"
	"testing"
)

func TestLeaseRenewalPreservesBootstrapIdentityLabels(t *testing.T) {
	raw := []byte(`{"apiVersion":"coordination.k8s.io/v1","kind":"Lease","metadata":{"name":"owned-lease","namespace":"owned-installation","uid":"native-uid","resourceVersion":"1","labels":{"ops.platform.io/managed-by":"opsctl-bootstrap","ops.platform.io/installation-id":"native-installation"},"annotations":{"ops.platform/owner-epoch":"0"}},"spec":{"leaseDurationSeconds":15}}`)
	var document LeaseDocument
	if json.Unmarshal(raw, &document) != nil {
		t.Fatal("invalid input")
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var after struct {
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	json.Unmarshal(encoded, &after)
	if after.Metadata.Labels["ops.platform.io/managed-by"] != "opsctl-bootstrap" || after.Metadata.Labels["ops.platform.io/installation-id"] != "native-installation" {
		t.Fatal("Graph Lease PUT discards formal installation identity labels")
	}
}
