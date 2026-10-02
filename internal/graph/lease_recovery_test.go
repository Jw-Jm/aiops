package graph

import (
	"context"
	"encoding/json"
	"errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	"net/http/httptest"
	"ops-platform/internal/integrations/kubernetes"
	"testing"
	"time"
)

type recoveryMirror struct {
	previous      OwnershipMirror
	begin, finish int
	floor         int64
	finishError   bool
}

func (r *recoveryMirror) Load(context.Context, string, string) (OwnershipMirror, error) {
	return r.previous, nil
}
func (r *recoveryMirror) BeginRecovery(_ context.Context, _ LeaseRecoveryRequest, subject string, _ OwnershipMirror, floor int64) (string, error) {
	if subject != "authenticated-operator" {
		return "", ErrScope
	}
	r.begin++
	r.floor = floor
	return "durable-intent", nil
}
func (r *recoveryMirror) FinishRecovery(_ context.Context, _ LeaseRecoveryRequest, _ string, _ string, _ OwnershipMirror, receipt LeaseRecoveryReceipt) error {
	r.finish++
	if r.finishError {
		return errors.New("database receipt unavailable")
	}
	if receipt.EpochFloor != r.floor {
		return ErrStale
	}
	return nil
}
func TestLeaseRecoveryFailsClosedForUnknownFloorLiveHolderAndCAS(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		floor                            int64
		live, wrongCluster, cas, receipt bool
	}{
		{name: "unknown-floor"}, {name: "live-holder", floor: 7, live: true}, {name: "wrong-cluster", floor: 7, wrongCluster: true}, {name: "native-CAS-conflict", floor: 7, cas: true}, {name: "receipt-failure", floor: 7, receipt: true}, {name: "expired-authority", floor: 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &recoveryMirror{previous: OwnershipMirror{Epoch: tc.floor, LeaseUID: "previous-uid"}, finishError: tc.receipt}
			mutations := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				enc := json.NewEncoder(w)
				if r.URL.Path == "/apis/authentication.k8s.io/v1/selfsubjectreviews" {
					w.WriteHeader(201)
					enc.Encode(map[string]any{"status": map[string]any{"userInfo": map[string]any{"username": "authenticated-operator"}}})
					return
				}
				if r.URL.Path == "/api/v1/namespaces/kube-system" {
					uid := "cluster-a"
					if tc.wrongCluster {
						uid = "other-cluster"
					}
					enc.Encode(map[string]any{"metadata": map[string]string{"uid": uid}})
					return
				}
				if r.Method == "GET" {
					d := LeaseDocument{}
					d.Metadata.UID = "recreated-uid"
					d.Metadata.ResourceVersion = "opaque-version"
					d.Metadata.Annotations = map[string]string{"ops.platform/owner-epoch": "12"}
					d.Spec.LeaseDurationSeconds = 15
					d.Spec.RenewTime = metav1.NewMicroTime(time.Now().Add(-time.Minute))
					if tc.live {
						d.Spec.HolderIdentity = "live-worker"
						d.Spec.RenewTime = metav1.NewMicroTime(time.Now())
					}
					enc.Encode(d)
					return
				}
				mutations++
				if repo.begin != 1 {
					t.Error("mutation before durable intent")
				}
				if tc.cas {
					w.WriteHeader(409)
					return
				}
				var d LeaseDocument
				if json.NewDecoder(r.Body).Decode(&d) != nil {
					t.Error("invalid Lease request")
				}
				if d.Spec.HolderIdentity != "" || d.Metadata.ResourceVersion != "opaque-version" {
					t.Error("repair elected owner or lost CAS")
				}
				enc.Encode(d)
			}))
			defer server.Close()
			client, _ := kubernetes.NewClient(server.URL, server.Client(), 20, 50)
			receipt, err := RecoverLease(context.Background(), client, repo, LeaseRecoveryRequest{Tenant: "tenant-a", Cluster: "cluster-a", Namespace: "owned", Name: "graph", ExpectedPreviousLeaseUID: "previous-uid", Ticket: "reviewed-repair"})
			if tc.name == "expired-authority" {
				if err != nil || receipt.EpochFloor != 13 || repo.finish != 1 {
					t.Fatalf("recovery=%+v %v", receipt, err)
				}
			} else if err == nil {
				t.Fatal("unsafe recovery accepted")
			}
			if tc.floor == 0 || tc.live || tc.wrongCluster {
				if mutations != 0 || repo.begin != 0 {
					t.Fatal("rejected recovery mutated authority")
				}
			}
			if tc.cas && repo.finish != 0 {
				t.Fatal("conflict committed receipt")
			}
			if tc.receipt && (receipt.LeaseUID != "recreated-uid" || repo.finish != 1) {
				t.Fatal("authority repair lost pending receipt")
			}
		})
	}
}
