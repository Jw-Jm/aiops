package resourcestore

import (
	"encoding/json"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/integrations/kubernetes"
	"strings"
	"testing"
	"time"
)

func TestHistoryProjectionExcludesSensitiveFields(t *testing.T) {
	raw := unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"uid": "uid", "name": "p", "resourceVersion": "1", "annotations": map[string]any{"secret": "CANARY"}}, "spec": map[string]any{"containers": []any{map[string]any{"name": "c", "command": []any{"CANARY"}, "env": []any{map[string]any{"name": "TOKEN", "value": "CANARY"}}}}}, "status": map[string]any{"containerStatuses": []any{map[string]any{"name": "c", "containerID": "containerd://a", "restartCount": int64(3), "state": map[string]any{"waiting": map[string]any{"reason": "CrashLoopBackOff", "message": "CANARY"}}, "lastState": map[string]any{"terminated": map[string]any{"reason": "OOMKilled", "exitCode": int64(137), "finishedAt": "2026-10-15T01:00:00Z", "message": "CANARY"}}}}}}}
	projected, err := kubernetes.Project(raw, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(HistoryProjection(projected))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if strings.Contains(s, "CANARY") || !strings.Contains(s, "OOMKilled") || !strings.Contains(s, "CrashLoopBackOff") || !strings.Contains(s, "containerd://a") || !strings.Contains(s, `"restartCount":3`) {
		t.Fatal(s)
	}
}
