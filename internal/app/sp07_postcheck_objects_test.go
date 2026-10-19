package app

import (
	"encoding/json"
	"errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/rca"
	"ops-platform/internal/resource"
	"strings"
	"testing"
	"time"
)

func TestNativePostCheckCollectionFencesAndProjection(t *testing.T) {
	var pod unstructured.Unstructured
	if json.Unmarshal([]byte(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"pod","uid":"pod-uid","namespace":"ns","resourceVersion":"1","annotations":{"credential":"secret-value"}},"spec":{"containers":[{"name":"app","image":"image:v1","env":[{"name":"PASSWORD","value":"secret-value"}]}]},"status":{"containerStatuses":[{"name":"app","ready":true,"state":{"terminated":{"exitCode":1,"message":"secret-value"}}}]}}`), &pod) != nil {
		t.Fatal("fixture")
	}
	scope := graph.Scope{Tenant: "tenant", Cluster: "cluster", Namespaces: []string{"ns"}, AuthorizationRevision: "revision"}
	list := unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "EventList", "items": []any{}}}
	read := func(path string) (unstructured.Unstructured, error) {
		switch path {
		case "/api/v1/namespaces/ns/pods/pod":
			return pod, nil
		case "/api/v1/namespaces/ns/events?limit=41":
			return list, nil
		}
		return unstructured.Unstructured{}, errors.New("unexpected request")
	}
	objects, err := nativePostCheckObjects(pod, "tenant", "cluster", scope, read)
	if err != nil || len(objects) != 1 {
		t.Fatal(objects, err)
	}
	raw, _ := json.Marshal(postCheckProjection(pod).Object)
	if strings.Contains(string(raw), "secret-value") || strings.Contains(string(raw), "PASSWORD") {
		t.Fatal("secret entered post-check evidence")
	}
	projected := postCheckProjection(pod)
	digest := rca.SpecDigest(projected)
	pod.SetAnnotations(map[string]string{"other": "annotation"})
	if rca.SpecDigest(postCheckProjection(pod)) != digest {
		t.Fatal("metadata changed rollback configuration")
	}
	list.SetContinue("next")
	if _, err := nativePostCheckObjects(pod, "tenant", "cluster", scope, read); err == nil {
		t.Fatal("partial events accepted")
	}
	list.SetContinue("")
	changed := func(path string) (unstructured.Unstructured, error) {
		o, err := read(path)
		if o.GetKind() == "Pod" {
			o = *o.DeepCopy()
			o.SetUID("recreated")
		}
		return o, err
	}
	if _, err := nativePostCheckObjects(pod, "tenant", "cluster", scope, changed); err == nil {
		t.Fatal("recreated pod followed by name")
	}
	denied := scope
	denied.Namespaces = []string{"other"}
	if _, err := nativePostCheckObjects(pod, "tenant", "cluster", denied, read); err == nil {
		t.Fatal("revoked namespace accepted")
	}
}

func TestNativePostCheckProjectionRetainsExecutableHealthFacts(t *testing.T) {
	var pod unstructured.Unstructured
	if err := json.Unmarshal([]byte(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"pod","uid":"pod-uid","namespace":"ns","resourceVersion":"1"},"spec":{"containers":[{"name":"app","image":"image:v1","env":[{"name":"SECRET","value":"private"}]}],"initContainers":[{"name":"init","image":"init:v1"}]},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}],"containerStatuses":[{"name":"app","ready":true,"state":{"running":{}}}],"initContainerStatuses":[{"name":"init","state":{"terminated":{"exitCode":0,"message":"private"}}}]}}`), &pod); err != nil {
		t.Fatal(err)
	}
	projected := postCheckProjection(pod)
	raw, _ := json.Marshal(projected.Object)
	if strings.Contains(string(raw), "private") {
		t.Fatal("private data retained")
	}
	completed := time.Now().UTC()
	now := completed.Add(time.Second)
	e := evidence.Evidence{TenantID: "tenant", ResourceCanonicalID: resource.CanonicalID{Domain: "k8s", Tenant: "tenant", Scope: "cluster", APIGroup: "core", Kind: "Pod", StableID: "pod-uid"}.String(), SourceSystem: "kubernetes", QueryTemplateVersion: "command-kubernetes-post-check/v1", TimeReliable: true, ReplayState: "archived_verified", ObservedFrom: now, ObservedTo: now, EvaluatedAt: now, Data: raw, ContentDigest: evidence.Digest(raw)}
	recipe, _ := rca.Builtin("pod-health")
	result, err := rca.NativePostCheckResources(recipe, []evidence.Evidence{e}, completed)
	if err != nil || result.Verdict != "resolved" || result.SpecDigest == "" {
		t.Fatal(result, err)
	}
}
