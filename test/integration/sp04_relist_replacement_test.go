package integration

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/resource"
	"ops-platform/internal/resourcestore"
)

func TestSP04RelistNodeReplacement(t *testing.T) {
	for _, tc := range []struct {
		name            string
		nativeError     string
		oldStillPresent bool
		replace         bool
	}{
		{name: "complete relist replaces old UID", replace: true},
		{name: "unverified native list cannot retire", nativeError: "watch_unavailable"},
		{name: "filtered native UID cannot retire", oldStillPresent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, db, pool, b := sp05Database(t)
			store := resourcestore.Repository{Pool: pool, ExpectedRevision: b.RegistrationRevision, BackendLogicalID: "sp05-native"}
			snapshot := func(uid, rv string) kubernetes.Snapshot {
				now := time.Now().UTC()
				o, err := kubernetes.Project(unstructured.Unstructured{Object: map[string]any{
					"apiVersion": "v1", "kind": "Node",
					"metadata": map[string]any{"name": "replacement-node", "uid": uid, "resourceVersion": rv},
					"spec":     map[string]any{"providerID": "cloud://same-machine"},
				}}, now)
				if err != nil {
					t.Fatal(err)
				}
				return kubernetes.Snapshot{GVR: kubernetes.GVR{Version: "v1", Resource: "nodes"}, Objects: []unstructured.Unstructured{o}, State: kubernetes.GVRState{LastListCompletedAt: now}}
			}
			oldUID, newUID := uuid.NewString(), uuid.NewString()
			old := snapshot(oldUID, "1")
			previous, err := store.SyncSnapshot(ctx, b.TenantID.String(), b.SourceID.String(), b.ClusterUID, "Node", "core", old, old, nil)
			if err != nil {
				t.Fatal("initial snapshot", err)
			}
			visible := snapshot(newUID, "2")
			native := visible
			native.State.LastError = tc.nativeError
			if tc.oldStillPresent {
				native.Objects = append(native.Objects, old.Objects[0])
			}
			next, err := store.SyncSnapshot(ctx, b.TenantID.String(), b.SourceID.String(), b.ClusterUID, "Node", "core", visible, native, previous)
			if (err == nil) != tc.replace {
				t.Fatalf("replacement=%v error=%v", tc.replace, err)
			}
			canonical := func(uid string) string {
				return (resource.CanonicalID{Domain: "k8s", Tenant: b.TenantID.String(), Scope: b.ClusterUID, APIGroup: "core", Kind: "Node", StableID: uid}).String()
			}
			var retired bool
			if err := db.QueryRowContext(ctx, `SELECT deleted_at IS NOT NULL FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2`, b.TenantID, canonical(oldUID)).Scan(&retired); err != nil || retired != tc.replace {
				t.Fatalf("old identity retired=%v error=%v", retired, err)
			}
			if !tc.replace {
				return
			}
			if _, exists := next[oldUID]; exists || next[newUID] != "2" {
				t.Fatalf("replacement acknowledgement: %v", next)
			}
			if _, err = store.SyncSnapshot(ctx, b.TenantID.String(), b.SourceID.String(), b.ClusterUID, "Node", "core", visible, native, next); err != nil {
				t.Fatal("replay", err)
			}
			var active, conflicts, observations int
			if err = db.QueryRowContext(ctx, `SELECT count(*) FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2 AND deleted_at IS NULL`, b.TenantID, canonical(newUID)).Scan(&active); err != nil || active != 1 {
				t.Fatalf("new identity active=%d error=%v", active, err)
			}
			if err = db.QueryRowContext(ctx, `SELECT count(*) FROM finding.records WHERE tenant_id=$1`, b.TenantID).Scan(&conflicts); err != nil || conflicts != 0 {
				t.Fatalf("spurious conflict findings=%d error=%v", conflicts, err)
			}
			if err = db.QueryRowContext(ctx, `SELECT count(*) FROM platform.resource_observations WHERE tenant_id=$1`, b.TenantID).Scan(&observations); err != nil || observations != 2 {
				t.Fatalf("history observations=%d error=%v", observations, err)
			}
		})
	}
}
