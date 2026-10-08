package kubernetes

import (
	"encoding/json"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"os"
	"slices"
	"testing"
	"time"
)

func TestFrozenOfficialMetricAndControlPlaneLabels(t *testing.T) {
	raw, err := os.ReadFile("../../../test/fixtures/inspection/kubernetes/observations-v1/labels.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Clock time.Time `json:"clock"`
		Cases []struct {
			Name, Domain                       string
			Usage, Allocatable                 map[string]any
			Expected                           []string
			Degraded                           bool
			OffsetSeconds, Status              int
			MetricName, MetricKind, ErrorClass string
		}
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	counts := map[string][3]int{}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			node := unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Node", "metadata": map[string]any{"name": "node-a", "uid": "uid-a", "resourceVersion": "1", "creationTimestamp": fixture.Clock.Add(-time.Hour).Format(time.RFC3339Nano)}, "status": map[string]any{"allocatable": c.Allocatable, "capacity": c.Allocatable}}}
			var got []Candidate
			var err error
			if c.Domain == "metrics" {
				name, kind := c.MetricName, c.MetricKind
				if name == "" {
					name = "node-a"
				}
				if kind == "" {
					kind = "NodeMetrics"
				}
				metrics := unstructured.Unstructured{Object: map[string]any{"apiVersion": "metrics.k8s.io/v1beta1", "kind": kind, "metadata": map[string]any{"name": name}, "timestamp": fixture.Clock.Add(time.Duration(c.OffsetSeconds) * time.Second).Format(time.RFC3339Nano), "window": "30s", "usage": c.Usage}}
				got, err = MetricSignals(node, metrics, fixture.Clock)
			} else {
				got, err = ControlPlaneSignals(node, c.Status, c.ErrorClass, fixture.Clock)
			}
			if (err != nil) != c.Degraded {
				t.Fatalf("degraded=%v error=%v", c.Degraded, err)
			}
			actual := []string{}
			for _, candidate := range got {
				if candidate.State == "firing" {
					actual = append(actual, candidate.NormalizedSymptom)
				}
			}
			slices.Sort(actual)
			slices.Sort(c.Expected)
			if !slices.Equal(actual, c.Expected) {
				t.Errorf("frozen symptoms expected=%v actual=%v", c.Expected, actual)
			}
			n := counts[c.Domain]
			for _, v := range actual {
				if slices.Contains(c.Expected, v) {
					n[0]++
				} else {
					n[1]++
				}
			}
			for _, v := range c.Expected {
				if !slices.Contains(actual, v) {
					n[2]++
				}
			}
			counts[c.Domain] = n
		})
	}
	for domain, n := range counts {
		p, r := float64(n[0])/float64(n[0]+n[1]), float64(n[0])/float64(n[0]+n[2])
		t.Logf("domain=%s TP=%d FP=%d FN=%d precision=%.3f recall=%.3f", domain, n[0], n[1], n[2], p, r)
		if p < .95 || r < .90 {
			t.Error("frozen per-domain acceptance failed")
		}
	}
}
