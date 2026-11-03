package observationquery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/contract"
	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/kubernetes"
)

func TestNativeLogsUseCurrentContainerAndRejectReplacement(t *testing.T) {
	request := validRequest()
	target := request.Resource
	target.Name = "web"
	scope := graph.Scope{Tenant: "11111111-1111-4111-8111-111111111111", Cluster: target.ClusterUID, Namespaces: []string{"apps"}, Resources: []string{target.CanonicalID}}
	uid, containerID, logStatus, replace := "pod-first", "containerd://original", 200, false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/namespaces/apps/pods/web" {
			_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"name": "web", "namespace": "apps", "uid": uid}, "spec": map[string]any{"containers": []any{map[string]string{"name": "web"}}}, "status": map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{"name": "web", "containerID": containerID, "restartCount": 0, "state": map[string]any{"running": map[string]any{"startedAt": request.Window.From.Add(-time.Hour)}}}}}})
			return
		}
		if r.URL.Path != "/api/v1/namespaces/apps/pods/web/log" {
			t.Errorf("escaped native scope %s", r.URL)
			w.WriteHeader(404)
			return
		}
		if r.URL.Query().Get("container") != "web" || r.URL.Query().Get("timestamps") != "true" || r.URL.Query().Has("follow") || r.URL.Query().Has("previous") {
			t.Errorf("unsafe log parameters %s", r.URL)
		}
		w.WriteHeader(logStatus)
		if logStatus != 200 || r.URL.Query().Get("tailLines") == "0" {
			return
		}
		if r.URL.Query().Get("limitBytes") != "65536" || r.URL.Query().Get("sinceTime") != request.Window.From.Format(time.RFC3339Nano) {
			t.Errorf("unbounded or wrong window %s", r.URL)
		}
		fmt.Fprintf(w, "%s info actual password=private-secret\n", request.Window.From.Add(time.Second).Format(time.RFC3339Nano))
		if replace {
			uid = "replacement"
		}
	}))
	defer server.Close()
	client, err := kubernetes.NewClient(server.URL, server.Client(), 20, 20)
	if err != nil {
		t.Fatal(err)
	}
	native := KubernetesLogs{Client: client, SourceID: uuid.New(), Revision: 3}
	cap, err := native.Capability(context.Background(), scope, target)
	if err != nil {
		t.Fatal(err)
	}
	request.Source = cap.Source
	request.Source.CredentialRevision = 1
	result, err := native.Read(context.Background(), scope, target, request)
	if err != nil || len(result.Samples) != 1 || strings.Contains(result.Samples[0].Message, "private-secret") {
		t.Fatalf("real log read/redaction: %+v %v", result, err)
	}
	later, err := native.Capability(context.Background(), scope, target)
	if err != nil || later.Source != cap.Source {
		t.Fatalf("wall clock changed stable source binding: %+v %v", later, err)
	}
	containerID = "containerd://restarted"
	if _, err = native.Read(context.Background(), scope, target, request); !errors.Is(err, ErrDenied) {
		t.Fatal("old container binding accepted", err)
	}
	containerID = "containerd://original"
	replace = true
	if _, err = native.Read(context.Background(), scope, target, request); !errors.Is(err, ErrDenied) {
		t.Fatal("replacement during read accepted", err)
	}
	uid = "pod-first"
	replace = false
	logStatus = 403
	if _, err = native.Capability(context.Background(), scope, target); err == nil {
		t.Fatal("pods/log denial exposed capability")
	}
	scope.Namespaces = nil
	if _, err = native.Capability(context.Background(), scope, target); !errors.Is(err, ErrDenied) {
		t.Fatal("namespace denial accepted", err)
	}
}
func TestNativeLogsSamplesNeverBecomePopulationOrMeasuredZero(t *testing.T) {
	request := validRequest()
	request.Source.CollectorVersion = NativeLogVersion
	request.Budget.MaxRows = 1
	line := func(at time.Time, text string) string { return at.Format(time.RFC3339Nano) + " " + text + "\n" }
	raw := line(request.Window.From.Add(-time.Nanosecond), "info before") + line(request.Window.From, "info first") + line(request.Window.To.Add(-time.Nanosecond), "ERROR last") + line(request.Window.To, "info outside")
	samples, err := parseNativeLogs([]byte(raw), request)
	if err != nil || len(samples.Samples) != 1 || samples.Samples[0].Message != "ERROR last" || samples.Samples[0].Level != "error" || !samples.Truncated {
		t.Fatalf("half-open window/latest budget: %+v %v", samples, err)
	}
	rec := Record{View: View{QueryID: uuid.New(), Resource: request.Resource, Source: request.Source, Window: request.Window, Coverage: request.Coverage(), ExpiresAt: time.Now().Add(time.Hour), Reauthorization: Reauthorization{CheckedAt: time.Now(), AuthorizationVersion: 1, SourceRegistrationRevision: 1, Result: "allowed"}}, Request: request}
	for _, examples := range []analytics.QueryResult{samples, {Samples: []analytics.LogSample{}}} {
		result, err := buildResult(rec, "partial", map[string]any{"basis": "not_measured", "method": nil}, examples)
		if err != nil {
			t.Fatal(err)
		}
		if err = contract.Validate("https://ops.local/schemas/ui-v12-observability/v1#/$defs/resultView", result); err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			Status     string
			Population map[string]any
			Truncation struct{ Reasons []string }
		}
		_ = json.Unmarshal(result, &decoded)
		if decoded.Status != "partial" || decoded.Population["basis"] != "not_measured" {
			t.Fatalf("unknown population became zero %s", result)
		}
		if examples.Truncated && (len(decoded.Truncation.Reasons) != 1 || decoded.Truncation.Reasons[0] != "source_partial") {
			t.Fatal("native truncation mislabeled", decoded.Truncation.Reasons)
		}
		if _, ok := decoded.Population["count"]; ok {
			t.Fatal("native sample count became population")
		}
	}
	request.Filters = []analytics.Filter{{Field: "level", Operator: "eq", Value: "debug"}}
	samples, err = parseNativeLogs([]byte(raw), request)
	if err != nil || len(samples.Samples) != 0 {
		t.Fatal("level filter", err)
	}
	if _, err = parseNativeLogs([]byte("missing-native-timestamp\n"), request); err == nil {
		t.Fatal("unattributed log accepted")
	}
	if _, err = parseNativeLogs([]byte{0xff}, request); err == nil {
		t.Fatal("invalid UTF8 accepted")
	}
}

func TestRetiredSourceHistoryDoesNotProbeAnotherNativeSource(t *testing.T) {
	service := Service{NativeLogs: &KubernetesLogs{SourceID: uuid.New()}}
	if err := service.validateBindings(context.Background(), graph.Claims{}, validRequest(), nil); !errors.Is(err, ErrUnavailable) {
		t.Fatal("retired source did not fail before source IO", err)
	}
}
func TestNativeLogsByteBoundDoesNotPublishPartialLine(t *testing.T) {
	request := validRequest()
	line := request.Window.From.Format(time.RFC3339Nano) + " info complete\n"
	raw := []byte(line + strings.Repeat("x", (64<<10)-len(line)))
	samples, err := parseNativeLogs(raw, request)
	if err != nil || len(samples.Samples) != 1 || samples.Samples[0].Message != "info complete" || !samples.Truncated {
		t.Fatalf("partial byte boundary: %+v %v", samples, err)
	}
}
