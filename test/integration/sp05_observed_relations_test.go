package integration

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/resource"
	"ops-platform/internal/resourcestore"
)

// The frozen relations of an observation name the exact identity of the other
// endpoint: the anchor's cluster and the API group of the endpoint are part of
// the lookup, an owner is bound by its UID, and a same-named object of another
// cluster, another API group or a later observation is never linked.
func TestObservedRelationsUseTheExactIdentity(t *testing.T) {
	ctx, db, pool, _ := sp05Database(t)
	tenant := uuid.New()
	clusterA, clusterB, clusterWild := uuid.New(), uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES($1,'sp05-relations','Relations')`, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.cluster_registrations(tenant_id,cluster_id,cluster_uid,display_name) VALUES($1,$2,'cluster-a','Same Name'),($1,$3,'cluster-b','Same Name'),($1,$4,'cluster_wild','Wildcard')`, tenant, clusterA, clusterB, clusterWild); err != nil {
		t.Fatal(err)
	}
	sourceA := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.source_registrations(tenant_id,source_id,cluster_id,source_type,instance_key,auth_ref,backend_logical_id) VALUES($1,$2,$3,'kubernetes','relations-a','openbao://test/a','kubernetes-a')`, tenant, sourceA, clusterA); err != nil {
		t.Fatal(err)
	}
	repo := resourcestore.Repository{Pool: pool, ExpectedRevision: 1, BackendLogicalID: "kubernetes-a"}

	observedAt := time.Now().UTC().Add(-time.Minute)
	insertEntity := func(t *testing.T, clusterID uuid.UUID, clusterUID, apiGroup, kind, name, uid, namespace string, at time.Time) string {
		t.Helper()
		canonical := resource.CanonicalID{Domain: "k8s", Tenant: tenant.String(), Scope: clusterUID, APIGroup: apiGroup, Kind: kind, StableID: uid}.String()
		if _, err := db.ExecContext(ctx, `INSERT INTO platform.resource_entities(tenant_id,canonical_id,cluster_id,kind,namespace,name,metadata,observed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, tenant, canonical, clusterID, kind, namespace, name, `{"sourceValues":{"uid":"`+uid+`"}}`, at); err != nil {
			t.Fatalf("insert %s %s: %v", kind, name, err)
		}
		return canonical
	}
	// The owner of the anchor cluster, and the same name in another cluster.
	ownerCanonical := insertEntity(t, clusterA, "cluster-a", "apps", "ReplicaSet", "rs-a", "rs-a-uid", "apps", observedAt.Add(-time.Minute))
	insertEntity(t, clusterB, "cluster-b", "apps", "ReplicaSet", "rs-a", "rs-b-uid", "apps", observedAt.Add(-time.Minute))
	// The node of the anchor cluster, the same name in another cluster and a
	// cluster whose scope carries a wildcard character.
	nodeCanonical := insertEntity(t, clusterA, "cluster-a", "core", "Node", "node-a", "node-a-uid", "", observedAt.Add(-time.Minute))
	insertEntity(t, clusterB, "cluster-b", "core", "Node", "node-a", "node-b-uid", "", observedAt.Add(-time.Minute))
	insertEntity(t, clusterWild, "cluster_wild", "core", "Node", "node-a", "node-wild-uid", "", observedAt.Add(-time.Minute))
	// A node of the same cluster that was observed after this observation.
	insertEntity(t, clusterA, "cluster-a", "core", "Node", "node-late", "node-late-uid", "", observedAt.Add(time.Minute))
	// Two nodes of the same cluster and the same name: a genuine ambiguity.
	insertEntity(t, clusterA, "cluster-a", "core", "Node", "node-ambiguous", "node-ambiguous-1", "", observedAt.Add(-time.Minute))
	insertEntity(t, clusterA, "cluster-a", "core", "Node", "node-ambiguous", "node-ambiguous-2", "", observedAt.Add(-time.Minute))
	// The claim and the volume of the anchor cluster.
	claimCanonical := insertEntity(t, clusterA, "cluster-a", "core", "PersistentVolumeClaim", "pvc-a", "pvc-a-uid", "apps", observedAt.Add(-time.Minute))
	insertEntity(t, clusterB, "cluster-b", "core", "PersistentVolumeClaim", "pvc-a", "pvc-b-uid", "apps", observedAt.Add(-time.Minute))
	volumeCanonical := insertEntity(t, clusterA, "cluster-a", "core", "PersistentVolume", "pv-a", "pv-a-uid", "", observedAt.Add(-time.Minute))
	// An owner of another API group with the same name.
	jobCanonical := insertEntity(t, clusterA, "cluster-a", "batch", "Job", "job-a", "job-a-uid", "apps", observedAt.Add(-time.Minute))
	insertEntity(t, clusterA, "cluster-a", "core", "Job", "job-a", "job-core-uid", "apps", observedAt.Add(-time.Minute))

	observe := func(t *testing.T, pod unstructured.Unstructured) []resource.Relation {
		t.Helper()
		snapshot := kubernetes.Snapshot{
			GVR:     kubernetes.GVR{Version: "v1", Resource: "pods", Kind: "Pod"},
			State:   kubernetes.GVRState{LastListCompletedAt: observedAt},
			Objects: []unstructured.Unstructured{pod},
		}
		if _, err := repo.SyncSnapshot(ctx, tenant.String(), sourceA.String(), "cluster-a", "Pod", "core", snapshot, snapshot, nil); err != nil {
			t.Fatalf("sync the observed pod: %v", err)
		}
		canonical := resource.CanonicalID{Domain: "k8s", Tenant: tenant.String(), Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: string(pod.GetUID())}.String()
		var projection []byte
		if err := db.QueryRowContext(ctx, `SELECT projection FROM platform.resource_observations WHERE tenant_id=$1 AND canonical_id=$2`, tenant, canonical).Scan(&projection); err != nil {
			t.Fatalf("read the observation: %v", err)
		}
		var stored struct {
			Relations []resource.Relation `json:"relations"`
		}
		if err := json.Unmarshal(projection, &stored); err != nil {
			t.Fatalf("decode the observation projection: %v", err)
		}
		return stored.Relations
	}
	makePod := func(uid string, mutate func(map[string]any)) unstructured.Unstructured {
		metadata := map[string]any{
			"name": "pod-" + uid, "namespace": "apps", "uid": uid, "resourceVersion": "10",
			"creationTimestamp": observedAt.Add(-time.Hour).Format(time.RFC3339),
			"annotations":       map[string]any{"ops.internal/observed-at": observedAt.Format(time.RFC3339Nano)},
		}
		spec := map[string]any{}
		if mutate != nil {
			mutate(metadata)
		}
		return unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "Pod",
			"metadata": metadata,
			"spec":     spec,
		}}
	}

	// The positive case: the owner, the node, the claim and the volume of the
	// anchor cluster are linked, and nothing of another cluster is.
	pod := makePod("pod-a-uid", func(metadata map[string]any) {
		metadata["ownerReferences"] = []any{map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSet", "name": "rs-a", "uid": "rs-a-uid"}}
	})
	pod.Object["spec"] = map[string]any{
		"nodeName": "node-a",
		"volumes":  []any{map[string]any{"persistentVolumeClaim": map[string]any{"claimName": "pvc-a"}}},
	}
	relations := observe(t, pod)
	targets := map[string]string{}
	for _, relation := range relations {
		targets[relation.Kind] = relation.To
	}
	if targets["owned_by"] != ownerCanonical {
		t.Fatalf("the owner relation points to %q, want %q", targets["owned_by"], ownerCanonical)
	}
	if targets["scheduled_on"] != nodeCanonical {
		t.Fatalf("the node relation points to %q, want %q", targets["scheduled_on"], nodeCanonical)
	}
	if targets["uses"] != claimCanonical {
		t.Fatalf("the claim relation points to %q, want %q", targets["uses"], claimCanonical)
	}
	for _, relation := range relations {
		parsed, err := resource.ParseCanonicalID(relation.To)
		if err != nil {
			t.Fatalf("the relation target is not a canonical id: %v", err)
		}
		if parsed.Scope != "cluster-a" {
			t.Fatalf("a relation crossed the cluster border: %+v", relation)
		}
	}

	// The volume of the pod is bound by its name and its own namespace.
	volumePod := makePod("pod-volume-uid", nil)
	volumePod.Object["spec"] = map[string]any{"volumeName": "pv-a"}
	volumeRelations := observe(t, volumePod)
	volumeTarget := ""
	for _, relation := range volumeRelations {
		if relation.Kind == "bound_to" {
			volumeTarget = relation.To
		}
	}
	if volumeTarget != volumeCanonical {
		t.Fatalf("the volume relation points to %q, want %q", volumeTarget, volumeCanonical)
	}

	// An owner of another API group is linked with its own group, and the same
	// name of the core group is not used.
	groupPod := makePod("pod-group-uid", func(metadata map[string]any) {
		metadata["ownerReferences"] = []any{map[string]any{"apiVersion": "batch/v1", "kind": "Job", "name": "job-a", "uid": "job-a-uid"}}
	})
	groupRelations := observe(t, groupPod)
	groupTarget := ""
	for _, relation := range groupRelations {
		if relation.Kind == "owned_by" {
			groupTarget = relation.To
		}
	}
	if groupTarget != jobCanonical {
		t.Fatalf("the owner of another group points to %q, want %q", groupTarget, jobCanonical)
	}

	// A replaced owner UID is not linked to the object of the same name.
	replaced := makePod("pod-replaced-uid", func(metadata map[string]any) {
		metadata["ownerReferences"] = []any{map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSet", "name": "rs-a", "uid": "rs-replaced-uid"}}
	})
	for _, relation := range observe(t, replaced) {
		if relation.Kind == "owned_by" {
			t.Fatalf("a replaced owner UID was linked: %+v", relation)
		}
	}

	// A node of the same cluster that was observed later is not part of the
	// observation of the earlier point in time.
	late := makePod("pod-late-uid", nil)
	late.Object["spec"] = map[string]any{"nodeName": "node-late"}
	for _, relation := range observe(t, late) {
		if relation.Kind == "scheduled_on" {
			t.Fatalf("a node observed after the observation was linked: %+v", relation)
		}
	}

	// A genuine ambiguity inside the cluster is refused.
	ambiguous := makePod("pod-ambiguous-uid", nil)
	ambiguous.Object["spec"] = map[string]any{"nodeName": "node-ambiguous"}
	for _, relation := range observe(t, ambiguous) {
		if relation.Kind == "scheduled_on" {
			t.Fatalf("an ambiguous node was linked: %+v", relation)
		}
	}
}
