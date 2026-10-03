package k8sgpt

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"testing"
	"time"
)

func TestAnalyzerCandidatesPreferOfficialAndBindNativeUID(t *testing.T) {
	clock := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	pod := unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "web", "namespace": "apps", "uid": "pod-a"}, "status": map[string]any{"phase": "Running"}}}
	out := Output{Status: "ProblemDetected", Problems: 1, Results: []Result{{Kind: "Pod", Name: "apps/web", Error: []AnalyzerError{{Text: "a native container error", Sensitive: map[string]string{"password": "never archive"}}}}}}
	candidates, err := Candidates(out, []unstructured.Unstructured{pod}, "tenant", "cluster", clock)
	if err != nil || len(candidates) != 1 || candidates[0].NativeIdentity == "" || candidates[0].IndependenceGroup != "pod-a" || candidates[0].State != "firing" {
		t.Fatalf("native Analyzer result not bound: %+v %v", candidates, err)
	}
	if string(candidates[0].Data) == "" {
		t.Fatal("empty Candidate")
	}
	out.Results[0].Name = "other/web"
	if _, err := Candidates(out, []unstructured.Unstructured{pod}, "tenant", "cluster", clock); err == nil {
		t.Fatal("unbound result accepted")
	}
	out.Results[0].Name = "apps/web"
	pod.Object["status"] = map[string]any{"conditions": []any{map[string]any{"type": "PodScheduled", "status": "False", "reason": "Unschedulable"}}}
	candidates, err = Candidates(out, []unstructured.Unstructured{pod}, "tenant", "cluster", clock)
	if err != nil || len(candidates) != 0 {
		t.Fatalf("official symptom emitted a second Analyzer incident: %+v %v", candidates, err)
	}
	candidates, err = Candidates(Output{Status: "OK"}, []unstructured.Unstructured{pod}, "tenant", "cluster", clock)
	if err != nil || len(candidates) != 1 || candidates[0].State != "resolved" {
		t.Fatalf("completed healthy Analyzer cannot close previous generic occurrence: %+v %v", candidates, err)
	}
}
