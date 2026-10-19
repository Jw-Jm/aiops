package rca

import (
	"encoding/json"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"ops-platform/internal/contract"
	"ops-platform/internal/evidence"
	"ops-platform/internal/resource"
	"testing"
	"time"
)

func nativeFixture(raw string) unstructured.Unstructured {
	var o unstructured.Unstructured
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		panic(err)
	}
	return o
}
func healthyNativePod() unstructured.Unstructured {
	return nativeFixture(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"pod","uid":"pod-uid","namespace":"ns"},"spec":{"nodeName":"node","containers":[{"name":"app","image":"image:v1","volumeMounts":[{"name":"data","mountPath":"/data"}]}],"volumes":[{"name":"data","persistentVolumeClaim":{"claimName":"claim"}}]},"status":{"podIP":"10.0.0.1","phase":"Running","conditions":[{"type":"Ready","status":"True"}],"containerStatuses":[{"name":"app","ready":true,"state":{"running":{}}}]}}`)
}
func nativeFixtureEvidence(objects []unstructured.Unstructured, completed time.Time) []evidence.Evidence {
	out := []evidence.Evidence{}
	for _, o := range objects {
		raw, _ := json.Marshal(o.Object)
		group := o.GroupVersionKind().Group
		if group == "" {
			group = "core"
		}
		out = append(out, evidence.Evidence{TenantID: "tenant", ResourceCanonicalID: resource.CanonicalID{Domain: "k8s", Tenant: "tenant", Scope: "cluster", APIGroup: group, Kind: o.GetKind(), StableID: string(o.GetUID())}.String(), SourceSystem: "kubernetes", QueryTemplateVersion: "command-kubernetes-post-check/v1", ReplayState: "archived_verified", TimeReliable: true, ObservedFrom: completed.Add(time.Second), ObservedTo: completed.Add(time.Second), EvaluatedAt: completed.Add(2 * time.Second), ContentDigest: evidence.Digest(raw), Data: raw})
	}
	return out
}
func TestNativePostCheckWorkloadsAndEvidenceFences(t *testing.T) {
	completed := time.Now().UTC()
	pod := healthyNativePod()
	recipe, _ := Builtin("pod-health")
	if contract.Validate("https://ops.local/schemas/recipe-registry/v2", mustJSON(recipe)) != nil {
		t.Fatal("new recipe not admitted")
	}
	for _, tc := range []struct {
		name   string
		change func([]evidence.Evidence)
		want   string
	}{
		{"healthy", func([]evidence.Evidence) {}, "resolved"},
		{"stale", func(es []evidence.Evidence) { es[0].ObservedFrom = completed }, "inconclusive"},
		{"digest", func(es []evidence.Evidence) { es[0].ContentDigest = "tampered" }, "inconclusive"},
		{"foreign_uid", func(es []evidence.Evidence) {
			es[0].ResourceCanonicalID = resource.CanonicalID{Domain: "k8s", Tenant: "tenant", Scope: "cluster", APIGroup: "core", Kind: "Pod", StableID: "recreated"}.String()
		}, "inconclusive"},
		{"unarchived", func(es []evidence.Evidence) { es[0].ReplayState = "archive_pending" }, "inconclusive"},
		{"unready", func(es []evidence.Evidence) {
			o := pod.DeepCopy()
			o.Object["status"].(map[string]any)["containerStatuses"] = []any{map[string]any{"name": "app", "ready": false, "state": map[string]any{"waiting": map[string]any{"reason": "CrashLoopBackOff"}}}}
			es[0] = nativeFixtureEvidence([]unstructured.Unstructured{*o}, completed)[0]
		}, "not_resolved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			es := nativeFixtureEvidence([]unstructured.Unstructured{pod}, completed)
			tc.change(es)
			out, err := NativePostCheckResources(recipe, es, completed)
			if err != nil || out.Verdict != tc.want {
				t.Fatal(out, err)
			}
		})
	}
	deployment := nativeFixture(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"deploy","uid":"deployment-uid","namespace":"ns","generation":3},"spec":{"replicas":1,"template":{"spec":{"containers":[{"name":"app","image":"image:v1"}]}}},"status":{"observedGeneration":3,"replicas":1,"updatedReplicas":1,"readyReplicas":1,"availableReplicas":1,"conditions":[{"type":"Available","status":"True"}]}}`)
	rs := nativeFixture(`{"apiVersion":"apps/v1","kind":"ReplicaSet","metadata":{"name":"rs","uid":"rs-uid","namespace":"ns","ownerReferences":[{"apiVersion":"apps/v1","kind":"Deployment","name":"deploy","uid":"deployment-uid","controller":true}]}}`)
	controller := true
	pod.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "rs", UID: types.UID("rs-uid"), Controller: &controller}})
	recipe, _ = Builtin("deployment-health")
	objects := []unstructured.Unstructured{deployment, rs, pod}
	out, err := NativePostCheckResources(recipe, nativeFixtureEvidence(objects, completed), completed)
	if err != nil || out.Verdict != "resolved" {
		t.Fatal(out, err)
	}
	_ = unstructured.SetNestedField(deployment.Object, int64(2), "status", "observedGeneration")
	objects[0] = deployment
	out, _ = NativePostCheckResources(recipe, nativeFixtureEvidence(objects, completed), completed)
	if out.Verdict != "inconclusive" {
		t.Fatal("old generation", out)
	}
	_ = unstructured.SetNestedField(deployment.Object, int64(3), "status", "observedGeneration")
	objects[0] = deployment
	pod.SetOwnerReferences(nil)
	objects[2] = pod
	out, _ = NativePostCheckResources(recipe, nativeFixtureEvidence(objects, completed), completed)
	if out.Verdict != "inconclusive" {
		t.Fatal("unrelated pod counted", out)
	}
}
func mustJSON(v any) []byte { raw, _ := json.Marshal(v); return raw }

func TestNativePostCheckServiceEndpointIdentityAndWarning(t *testing.T) {
	completed := time.Now().UTC()
	r, _ := Builtin("service-endpoints-health")
	service := nativeFixture(`{"apiVersion":"v1","kind":"Service","metadata":{"name":"svc","uid":"svc-uid","namespace":"ns"},"spec":{"ports":[{"port":80,"targetPort":8080}]}}`)
	slice := nativeFixture(`{"apiVersion":"discovery.k8s.io/v1","kind":"EndpointSlice","metadata":{"name":"slice","uid":"slice-uid","namespace":"ns","labels":{"kubernetes.io/service-name":"svc"},"ownerReferences":[{"apiVersion":"v1","kind":"Service","name":"svc","uid":"svc-uid","controller":true}]},"ports":[{"port":8080}],"endpoints":[{"addresses":["10.0.0.1"],"conditions":{"ready":true},"targetRef":{"kind":"Pod","namespace":"ns","name":"pod","uid":"pod-uid"}}]}`)
	objects := []unstructured.Unstructured{service, slice, healthyNativePod()}
	out, err := NativePostCheckResources(r, nativeFixtureEvidence(objects, completed), completed)
	if err != nil || out.Verdict != "resolved" {
		t.Fatal(out, err)
	}
	named := []unstructured.Unstructured{*service.DeepCopy(), *slice.DeepCopy(), healthyNativePod()}
	_ = unstructured.SetNestedSlice(named[0].Object, []any{map[string]any{"port": int64(80), "targetPort": "http"}}, "spec", "ports")
	containers := named[2].Object["spec"].(map[string]any)["containers"].([]any)
	containers[0].(map[string]any)["ports"] = []any{map[string]any{"name": "http", "containerPort": int64(8080)}}
	out, _ = NativePostCheckResources(r, nativeFixtureEvidence(named, completed), completed)
	if out.Verdict != "resolved" {
		t.Fatal("named target port", out)
	}
	containers[0].(map[string]any)["ports"].([]any)[0].(map[string]any)["containerPort"] = int64(9999)
	out, _ = NativePostCheckResources(r, nativeFixtureEvidence(named, completed), completed)
	if out.Verdict != "inconclusive" {
		t.Fatal("wrong named target port accepted", out)
	}
	// Same name cannot reconnect a recreated Pod to stale EndpointSlice identity.
	objects[2].SetUID("new-pod-uid")
	out, _ = NativePostCheckResources(r, nativeFixtureEvidence(objects, completed), completed)
	if out.Verdict != "inconclusive" {
		t.Fatal(out)
	}
	objects[2] = healthyNativePod()
	warning := nativeFixture(`{"apiVersion":"v1","kind":"Event","metadata":{"name":"new-warning","uid":"event-uid","namespace":"ns"},"type":"Warning","reason":"FailedMount","involvedObject":{"uid":"pod-uid"}}`)
	warning.Object["lastTimestamp"] = completed.Add(time.Second).Format(time.RFC3339Nano)
	objects = append(objects, warning)
	out, _ = NativePostCheckResources(r, nativeFixtureEvidence(objects, completed), completed)
	if out.Verdict != "not_resolved" || len(out.NewAnomalies) != 1 {
		t.Fatal("new warning hidden", out)
	}
	objects[3].Object["lastTimestamp"] = completed.Add(-time.Second).Format(time.RFC3339Nano)
	out, _ = NativePostCheckResources(r, nativeFixtureEvidence(objects, completed), completed)
	if out.Verdict != "resolved" {
		t.Fatal("old warning reinterpreted", out)
	}
}
func TestNativePostCheckPVCRequiresMountAndCSIAttachment(t *testing.T) {
	completed := time.Now().UTC()
	r, _ := Builtin("pvc-csi-failure")
	pvc := nativeFixture(`{"apiVersion":"v1","kind":"PersistentVolumeClaim","metadata":{"name":"claim","uid":"claim-uid","namespace":"ns"},"spec":{"volumeName":"pv"},"status":{"phase":"Bound"}}`)
	pv := nativeFixture(`{"apiVersion":"v1","kind":"PersistentVolume","metadata":{"name":"pv","uid":"pv-uid"},"spec":{"claimRef":{"name":"claim","namespace":"ns","uid":"claim-uid"},"csi":{"driver":"csi.example"}},"status":{"phase":"Bound"}}`)
	driver := nativeFixture(`{"apiVersion":"storage.k8s.io/v1","kind":"CSIDriver","metadata":{"name":"csi.example","uid":"driver-uid"},"spec":{"attachRequired":true}}`)
	va := nativeFixture(`{"apiVersion":"storage.k8s.io/v1","kind":"VolumeAttachment","metadata":{"name":"va","uid":"va-uid"},"spec":{"source":{"persistentVolumeName":"pv"},"nodeName":"node","attacher":"csi.example"},"status":{"attached":true}}`)
	objects := []unstructured.Unstructured{pvc, pv, driver, healthyNativePod(), va}
	out, err := NativePostCheckResources(r, nativeFixtureEvidence(objects, completed), completed)
	if err != nil || out.Verdict != "resolved" {
		t.Fatal(out, err)
	}
	out, _ = NativePostCheckResources(r, nativeFixtureEvidence(objects[:3], completed), completed)
	if out.Verdict != "inconclusive" {
		t.Fatal("Bound treated as mounted", out)
	}
	_ = unstructured.SetNestedField(va.Object, false, "status", "attached")
	objects[4] = va
	out, _ = NativePostCheckResources(r, nativeFixtureEvidence(objects, completed), completed)
	if out.Verdict != "not_resolved" {
		t.Fatal("unattached CSI recovered", out)
	}
	_ = unstructured.SetNestedField(driver.Object, false, "spec", "attachRequired")
	objects[2] = driver
	out, _ = NativePostCheckResources(r, nativeFixtureEvidence(objects[:4], completed), completed)
	if out.Verdict != "resolved" {
		t.Fatal("attachless CSI incorrectly blocked", out)
	}
	objects[3].Object["spec"].(map[string]any)["containers"] = []any{map[string]any{"name": "app", "image": "image:v1"}}
	out, _ = NativePostCheckResources(r, nativeFixtureEvidence(objects[:4], completed), completed)
	if out.Verdict != "inconclusive" {
		t.Fatal("unused PVC counted as mounted", out)
	}
}

func TestPostCheckOnlyRecipesCannotProduceRCACandidates(t *testing.T) {
	for _, name := range []string{"pod-health", "deployment-health", "service-endpoints-health"} {
		r, err := Builtin(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeRecipe(mustJSON(r)); err != nil {
			t.Fatal(err)
		}
		if _, err := Evaluate(r, Input{}); err == nil {
			t.Fatal("post-check declaration became an RCA adapter")
		}
	}
}
