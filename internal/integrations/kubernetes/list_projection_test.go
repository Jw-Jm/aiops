package kubernetes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestListAndWatchProduceIdenticalProjection(t *testing.T) {
	raw := json.RawMessage(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"p","namespace":"apps","uid":"p1","resourceVersion":"7"},"status":{"containerStatuses":[{"name":"main","restartCount":3,"ready":false,"state":{"terminated":{"exitCode":2,"signal":9}}}]}}`)
	var watch unstructured.Unstructured
	if err := json.Unmarshal(raw, &watch); err != nil {
		t.Fatal(err)
	}
	expected, err := Project(watch, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	expected.SetAnnotations(nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") == "true" {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]string{"resourceVersion": "7"}, "items": []json.RawMessage{raw}})
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client(), 20, 50)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	seen := false
	_ = client.Run(ctx, GVR{Version: "v1", Resource: "pods"}, []string{"apps"}, func(s Snapshot) error {
		if len(s.Objects) == 0 {
			return nil
		}
		seen = true
		actual := s.Objects[0].DeepCopy()
		actual.SetAnnotations(nil)
		if !reflect.DeepEqual(expected.Object, actual.Object) {
			t.Errorf("LIST and WATCH disagree for the same UID/RV:\nlist=%v\nwatch=%v", actual.Object, expected.Object)
		}
		statuses, _, _ := unstructured.NestedSlice(actual.Object, "status", "containerStatuses")
		if len(statuses) != 1 || statuses[0].(map[string]any)["restartCount"] != int64(3) {
			t.Error("container restart count was lost")
		}
		cancel()
		return nil
	})
	if !seen {
		t.Fatal("no list fact published")
	}
}
