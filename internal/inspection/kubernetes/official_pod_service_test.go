package kubernetes

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func projected(kind string, object map[string]any) unstructured.Unstructured {
	object["apiVersion"] = "v1"
	object["kind"] = kind
	return unstructured.Unstructured{Object: object}
}

func symptoms(o unstructured.Unstructured) map[string]string {
	out := map[string]string{}
	for _, c := range Signals(o) {
		out[c.NormalizedSymptom] = c.State
	}
	return out
}

func TestPodImagePullSignals(t *testing.T) {
	metadata := map[string]any{"uid": "pod-1", "name": "pull", "namespace": "apps", "resourceVersion": "9", "creationTimestamp": "2026-11-06T00:00:00Z"}
	waiting := func(reason string) map[string]any {
		return map[string]any{"state": map[string]any{"waiting": map[string]any{"reason": reason}}, "ready": false, "restartCount": int64(1)}
	}
	running := map[string]any{"state": map[string]any{"running": map[string]any{"startedAt": "2026-11-06T00:01:00Z"}}, "ready": true}
	if got := symptoms(projected("Pod", map[string]any{"metadata": metadata, "status": map[string]any{"containerStatuses": []any{waiting("ErrImagePull")}}})); got["ImagePullFailure"] != "firing" {
		t.Fatalf("ErrImagePull waiting not firing: %v", got)
	}
	if got := symptoms(projected("Pod", map[string]any{"metadata": metadata, "status": map[string]any{"containerStatuses": []any{waiting("ImagePullBackOff")}}})); got["ImagePullFailure"] != "firing" {
		t.Fatalf("ImagePullBackOff waiting not firing: %v", got)
	}
	if got := symptoms(projected("Pod", map[string]any{"metadata": metadata, "status": map[string]any{"containerStatuses": []any{waiting("ErrImageNeverPull")}}})); got["ImagePullFailure"] != "firing" {
		t.Fatalf("ErrImageNeverPull waiting not firing: %v", got)
	}
	if got := symptoms(projected("Pod", map[string]any{"metadata": metadata, "status": map[string]any{"containerStatuses": []any{waiting("ContainerCreating")}}})); len(got) != 0 {
		t.Fatalf("ContainerCreating must not fire pull signals: %v", got)
	}
	got := symptoms(projected("Pod", map[string]any{"metadata": metadata, "status": map[string]any{"containerStatuses": []any{running}}}))
	if got["ImagePullFailure"] != "resolved" || got["ImagePullError"] != "resolved" {
		t.Fatalf("running pod did not settle pull occurrences: %v", got)
	}
}

func TestKubeletPullEventSignal(t *testing.T) {
	event := func(reason, message, reporter string) unstructured.Unstructured {
		return projected("Event", map[string]any{
			"metadata":           map[string]any{"uid": "event-1", "name": "pull.1", "namespace": "apps", "resourceVersion": "3", "creationTimestamp": "2026-11-06T00:00:00Z"},
			"type":               "Warning",
			"reason":             reason,
			"message":            message,
			"reportingComponent": reporter,
			"involvedObject":     map[string]any{"kind": "Pod", "uid": "pod-1", "name": "pull", "namespace": "apps"},
			"firstTimestamp":     "2026-11-06T00:00:01Z",
		})
	}
	if got := symptoms(event("Failed", `Failed to pull image "registry.example.invalid/frontend:v": no such host`, "kubelet")); got["ImagePullError"] != "firing" {
		t.Fatalf("pull-failure event not firing: %v", got)
	}
	if got := symptoms(event("Failed", "Error: ErrImagePull", "kubelet")); got["ImagePullError"] != "firing" {
		t.Fatalf("ErrImagePull event not firing: %v", got)
	}
	if got := symptoms(event("Failed", "Error: ImagePullBackOff", "kubelet")); got["ImagePullError"] != "firing" {
		t.Fatalf("ImagePullBackOff event not firing: %v", got)
	}
	if got := symptoms(event("Failed", "Failed to start container wonderful", "kubelet")); len(got) != 0 {
		t.Fatalf("unrelated kubelet failure must not fire: %v", got)
	}
	if got := symptoms(event("Failed", `Failed to pull image "x"`, "daemonset-controller")); len(got) != 0 {
		t.Fatalf("non-kubelet pull event must not fire: %v", got)
	}
}

func TestEndpointSliceServiceSignal(t *testing.T) {
	slice := func(endpoints []any) unstructured.Unstructured {
		return projected("EndpointSlice", map[string]any{
			"metadata": map[string]any{"uid": "slice-1", "name": "web-1", "namespace": "apps", "resourceVersion": "7", "creationTimestamp": "2026-11-06T00:00:00Z", "ownerReferences": []any{map[string]any{"kind": "Service", "name": "web", "uid": "svc-1", "controller": true, "apiVersion": "v1"}}},
			"addressType": "IPv4",
			"endpoints":   endpoints,
		})
	}
	ready := map[string]any{"addresses": []any{"10.1.1.1"}, "conditions": map[string]any{"ready": true}, "targetRef": map[string]any{"kind": "Pod", "uid": "pod-1"}}
	unready := map[string]any{"addresses": []any{"10.1.1.2"}, "conditions": map[string]any{"ready": false}, "targetRef": map[string]any{"kind": "Pod", "uid": "pod-2"}}
	if got := symptoms(slice([]any{})); got["ServiceNoEndpoints"] != "firing" {
		t.Fatalf("empty slice must fire: %v", got)
	}
	if got := symptoms(slice([]any{unready})); got["ServiceNoEndpoints"] != "firing" {
		t.Fatalf("unready endpoints must fire: %v", got)
	}
	if got := symptoms(slice([]any{unready, ready})); got["ServiceNoEndpoints"] != "resolved" {
		t.Fatalf("one ready endpoint must resolve: %v", got)
	}
	for _, c := range Signals(slice([]any{})) {
		if c.ResourceKind != "Service" || c.ResourceUID != "svc-1" {
			t.Fatalf("signal must target the owning Service, got %s/%s", c.ResourceKind, c.ResourceUID)
		}
	}
}

func TestContainerFaultSignals(t *testing.T) {
	metadata := map[string]any{"uid": "pod-1", "name": "app", "namespace": "apps", "resourceVersion": "9", "creationTimestamp": "2026-11-07T00:00:00Z"}
	status := func(state, lastState map[string]any, ready bool) map[string]any {
		return map[string]any{"containerStatuses": []any{map[string]any{"state": state, "lastState": lastState, "ready": ready, "restartCount": int64(3)}}}
	}
	waiting := func(reason string) map[string]any { return map[string]any{"waiting": map[string]any{"reason": reason}} }
	terminated := func(reason string, code int64) map[string]any { return map[string]any{"terminated": map[string]any{"reason": reason, "exitCode": code}} }
	running := map[string]any{"running": map[string]any{"startedAt": "2026-11-07T00:01:00Z"}}

	if got := symptoms(projected("Pod", map[string]any{"metadata": metadata, "status": status(waiting("CrashLoopBackOff"), terminated("Error", 1), false)})); got["CrashLoopBackOff"] != "firing" {
		t.Fatalf("app crash backoff not firing: %v", got)
	}
	oom := symptoms(projected("Pod", map[string]any{"metadata": metadata, "status": status(waiting("CrashLoopBackOff"), terminated("OOMKilled", 137), false)}))
	if oom["OOMKilled"] != "firing" {
		t.Fatalf("OOM not attributed: %v", oom)
	}
	if _, present := oom["CrashLoopBackOff"]; present {
		t.Fatalf("OOM must not double-report crashloop: %v", oom)
	}
	current := symptoms(projected("Pod", map[string]any{"metadata": metadata, "status": status(terminated("OOMKilled", 137), nil, false)}))
	if current["OOMKilled"] != "firing" {
		t.Fatalf("current terminated OOM not firing: %v", current)
	}
	healthy := symptoms(projected("Pod", map[string]any{"metadata": metadata, "status": status(running, nil, true)}))
	for _, symptom := range []string{"CrashLoopBackOff", "OOMKilled", "ProbeFailure", "ContainerBackOffError"} {
		if healthy[symptom] != "resolved" {
			t.Fatalf("healthy pod must settle %s: %v", symptom, healthy)
		}
	}
}

func TestKubeletContainerAndProbeEventSignals(t *testing.T) {
	event := func(reason, message string) unstructured.Unstructured {
		return projected("Event", map[string]any{
			"metadata":           map[string]any{"uid": "event-1", "name": "app.1", "namespace": "apps", "resourceVersion": "3", "creationTimestamp": "2026-11-07T00:00:00Z"},
			"type":               "Warning",
			"reason":             reason,
			"message":            message,
			"reportingComponent": "kubelet",
			"involvedObject":     map[string]any{"kind": "Pod", "uid": "pod-1", "name": "app", "namespace": "apps"},
			"firstTimestamp":     "2026-11-07T00:00:01Z",
		})
	}
	if got := symptoms(event("BackOff", "Back-off restarting failed container app in pod apps/pod-1")); got["ContainerBackOffError"] != "firing" {
		t.Fatalf("backoff event not firing: %v", got)
	}
	if got := symptoms(event("Unhealthy", "Readiness probe failed: HTTP probe failed with statuscode: 500")); got["ProbeFailure"] != "firing" {
		t.Fatalf("readiness probe event not firing: %v", got)
	}
	if got := symptoms(event("Unhealthy", "Liveness probe failed: HTTP probe failed with statuscode: 503")); got["ProbeFailure"] != "firing" {
		t.Fatalf("liveness probe event not firing: %v", got)
	}
	got := symptoms(event("BackOff", "Back-off pulling image registry.example.invalid/frontend:v1"))
	if got["ImagePullError"] != "firing" {
		t.Fatalf("image backoff belongs to the pull family: %v", got)
	}
	if _, present := got["ContainerBackOffError"]; present {
		t.Fatalf("image backoff is not a container crash: %v", got)
	}
}

func TestDeploymentRolloutSignals(t *testing.T) {
	deployment := func(status, reason string) unstructured.Unstructured {
		return projected("Deployment", map[string]any{
			"metadata": map[string]any{"uid": "deploy-1", "name": "web", "namespace": "apps", "resourceVersion": "11", "creationTimestamp": "2026-11-07T00:00:00Z"},
			"status": map[string]any{"conditions": []any{map[string]any{
				"type": "Progressing", "status": status, "reason": reason, "lastTransitionTime": "2026-11-07T00:02:00Z"}}},
		})
	}
	if got := symptoms(deployment("False", "ProgressDeadlineExceeded")); got["RolloutStuck"] != "firing" {
		t.Fatalf("deadline exceeded not firing: %v", got)
	}
	if got := symptoms(deployment("True", "NewReplicaSetAvailable")); got["RolloutStuck"] != "resolved" {
		t.Fatalf("available rollout must settle: %v", got)
	}
	if got := symptoms(deployment("True", "ReplicaSetUpdated")); len(got) != 0 {
		t.Fatalf("in-progress rollout must be silent: %v", got)
	}
}

func TestCanonicalGroupForKinds(t *testing.T) {
	cases := map[string]string{
		"Pod": "core", "Service": "core", "Event": "core", "PersistentVolumeClaim": "core",
		"Deployment": "apps", "ReplicaSet": "apps", "StatefulSet": "apps", "DaemonSet": "apps", "Job": "apps",
		"StorageClass": "storage.k8s.io", "CSIDriver": "storage.k8s.io",
		"EndpointSlice": "discovery.k8s.io",
	}
	for kind, group := range cases {
		if got := CanonicalGroupFor(kind); got != group {
			t.Fatalf("CanonicalGroupFor(%s)=%s want %s", kind, got, group)
		}
	}
	c := Candidate{ResourceKind: "Deployment", ResourceUID: "deploy-uid"}
	if id := c.CanonicalID("tenant", "cluster"); !strings.Contains(id, "/apps/Deployment/deploy-uid") {
		t.Fatalf("deployment canonical must carry the apps group: %s", id)
	}
}
