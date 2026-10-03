package k8sgpt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	kube "ops-platform/internal/integrations/kubernetes"
	"strings"
	"testing"
)

func TestReadBrokerScopeRevocationAndSharedClient(t *testing.T) {
	reads := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		if r.URL.Path != "/api/v1/namespaces/apps/pods" || r.URL.Query().Get("labelSelector") != "team=ops" || r.URL.Query().Get("limit") != "500" {
			t.Errorf("unexpected upstream request: %s", r.URL.String())
		}
		json.NewEncoder(w).Encode(map[string]any{"kind": "PodList", "metadata": map[string]any{}, "items": []any{map[string]any{"metadata": map[string]any{"name": "web", "namespace": "apps", "uid": "pod-a", "labels": map[string]string{"team": "ops"}}}}})
	}))
	defer upstream.Close()
	client, _ := kube.NewClient(upstream.URL, upstream.Client(), 10, 25)
	revoked := false
	b := ReadBroker{Client: client, Namespace: "apps", RequiredLabels: map[string]string{"team": "ops"}, Token: "private-one-use-token", Authorize: func(context.Context) error {
		if revoked {
			return ErrDisabled
		}
		return nil
	}}
	for _, path := range []string{"/api/v1/namespaces/other/pods", "/api/v1/pods", "/api/v1/secrets", "/api/v1/namespaces/apps/pods?watch=true", "/api/v1/namespaces/apps/pods/web/exec", "/api/v1/namespaces/apps/pods?labelSelector=team%3Dother", "/api/v1/namespaces/apps/pods?limit=5000"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+b.Token)
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		if w.Code < 400 || reads != 0 {
			t.Fatalf("forbidden Analyzer request escaped: %s status=%d reads=%d", path, w.Code, reads)
		}
	}
	r := httptest.NewRequest("GET", "/api/v1/namespaces/apps/pods", nil)
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 403 || reads != 0 {
		t.Fatal("missing private invocation credential accepted")
	}
	r.Header.Set("Authorization", "Bearer "+b.Token)
	w = httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 200 || reads != 1 || !strings.Contains(w.Body.String(), "pod-a") {
		t.Fatalf("legitimate read failed %d %s", w.Code, w.Body.String())
	}
	revoked = true
	w = httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 403 || reads != 1 {
		t.Fatal("revocation was not checked before the shared client read")
	}
}

func TestReadBrokerRejectsPartialOrMisbindingResponse(t *testing.T) {
	for _, raw := range []string{
		`{"metadata":{"continue":"next"},"items":[]}`,
		`{"metadata":{},"items":[{"metadata":{"namespace":"other","uid":"x"}}]}`,
		`{"metadata":{},"items":[{"metadata":{"namespace":"apps","uid":"x","labels":{"team":"other"}}}]}`,
	} {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(raw)) }))
		client, _ := kube.NewClient(upstream.URL, upstream.Client(), 10, 25)
		b := ReadBroker{Client: client, Namespace: "apps", RequiredLabels: map[string]string{"team": "ops"}, Token: "test", Authorize: func(context.Context) error { return nil }}
		r := httptest.NewRequest("GET", "/api/v1/namespaces/apps/pods", nil)
		r.Header.Set("Authorization", "Bearer test")
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		upstream.Close()
		if w.Code != 503 {
			t.Fatalf("partial or misbound native result accepted: %d", w.Code)
		}
	}
}

func TestReadBrokerOnlyFollowsKnownNativeOwnerUIDs(t *testing.T) {
	for _, mode := range []string{"valid", "wrong-uid", "wrong-label", "unknown-owner", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			reads := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads++
				if r.URL.Path == "/api/v1/namespaces/apps/pods" {
					w.Write([]byte(`{"metadata":{},"items":[{"metadata":{"name":"web","namespace":"apps","uid":"pod-a","labels":{"team":"ops"},"ownerReferences":[{"apiVersion":"apps/v1","kind":"ReplicaSet","name":"web-rs","uid":"rs-a"}]}}]}`))
					return
				}
				if r.URL.Path != "/apis/apps/v1/namespaces/apps/replicasets/web-rs" || len(r.URL.Query()) != 0 {
					t.Errorf("parent read broadened: %s", r.URL.String())
				}
				uid, label := "rs-a", "ops"
				if mode == "wrong-uid" {
					uid = "recreated"
				}
				if mode == "wrong-label" {
					label = "other"
				}
				json.NewEncoder(w).Encode(map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSet", "metadata": map[string]any{"name": "web-rs", "namespace": "apps", "uid": uid, "labels": map[string]string{"team": label}}})
			}))
			defer upstream.Close()
			client, _ := kube.NewClient(upstream.URL, upstream.Client(), 10, 25)
			revoked := false
			b := ReadBroker{Client: client, Namespace: "apps", RequiredLabels: map[string]string{"team": "ops"}, Token: "test", Authorize: func(context.Context) error {
				if revoked {
					return ErrDisabled
				}
				return nil
			}}
			read := func(path string) int {
				r := httptest.NewRequest("GET", path, nil)
				r.Header.Set("Authorization", "Bearer test")
				w := httptest.NewRecorder()
				b.ServeHTTP(w, r)
				return w.Code
			}
			if read("/api/v1/namespaces/apps/pods") != 200 {
				t.Fatal("seed native Pod unavailable")
			}
			path := "/apis/apps/v1/namespaces/apps/replicasets/web-rs"
			if mode == "unknown-owner" {
				path = "/apis/apps/v1/namespaces/apps/replicasets/other"
			}
			if mode == "revoked" {
				revoked = true
			}
			code := read(path)
			if mode == "valid" {
				if code != 200 || reads != 2 {
					t.Fatalf("locked Analyzer native parent unavailable: code=%d reads=%d", code, reads)
				}
			} else if code < 400 {
				t.Fatalf("unsafe native owner accepted: %s", mode)
			}
			if (mode == "unknown-owner" || mode == "revoked") && reads != 1 {
				t.Fatal("unbound/revoked owner reached native API")
			}
		})
	}
}
