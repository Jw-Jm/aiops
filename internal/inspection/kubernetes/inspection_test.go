package kubernetes

import (
	"encoding/json"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"os"
	"testing"
)

func TestFrozenOfficialInspectionLabels(t *testing.T) {
	raw, err := os.ReadFile("../../../test/fixtures/inspection/kubernetes/labels-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Domain   string         `json:"domain"`
		Object   map[string]any `json:"object"`
		Expected []string       `json:"expected"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	counts := map[string][3]int{}
	for _, c := range cases {
		got := Inspect(unstructured.Unstructured{Object: c.Object})
		actual := map[string]bool{}
		for _, f := range got {
			actual[f.NormalizedSymptom] = true
		}
		expected := map[string]bool{}
		for _, s := range c.Expected {
			expected[s] = true
		}
		v := counts[c.Domain]
		for s := range actual {
			if expected[s] {
				v[0]++
			} else {
				v[1]++
			}
		}
		for s := range expected {
			if !actual[s] {
				v[2]++
			}
		}
		counts[c.Domain] = v
	}
	for domain, c := range counts {
		precision := float64(c[0]) / float64(c[0]+c[1])
		recall := float64(c[0]) / float64(c[0]+c[2])
		t.Logf("domain=%s TP=%d FP=%d FN=%d precision=%.3f recall=%.3f", domain, c[0], c[1], c[2], precision, recall)
		if precision < .95 || recall < .9 {
			t.Fatal("frozen domain gate failed")
		}
	}
}
