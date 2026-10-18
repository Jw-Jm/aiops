package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"ops-platform/internal/integrations/kubernetes"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type faultMirror struct {
	mu    sync.Mutex
	value OwnershipMirror
	fail  bool
}

func (m *faultMirror) Load(context.Context, string, string) (OwnershipMirror, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return OwnershipMirror{}, errors.New("mirror unavailable")
	}
	return m.value, nil
}
func (m *faultMirror) Record(_ context.Context, v OwnershipMirror) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return errors.New("mirror unavailable")
	}
	m.value = v
	return nil
}
func TestLeaseIsOnlyAuthorityRestartStandbyPartitionAndRecreation(t *testing.T) {
	var mu sync.Mutex
	version := 1
	status := 200
	doc := LeaseDocument{APIVersion: "coordination.k8s.io/v1", Kind: "Lease"}
	doc.Metadata.Name = "graph"
	doc.Metadata.Namespace = "owned"
	doc.Metadata.UID = "lease-original"
	doc.Metadata.ResourceVersion = "1"
	doc.Metadata.Annotations = map[string]string{"ops.platform/owner-epoch": "0"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if status != 200 {
			w.WriteHeader(status)
			return
		}
		if r.Method == "PUT" {
			var next LeaseDocument
			if json.NewDecoder(r.Body).Decode(&next) != nil || next.Metadata.ResourceVersion != doc.Metadata.ResourceVersion {
				w.WriteHeader(409)
				return
			}
			version++
			next.Metadata.ResourceVersion = strconv.Itoa(version)
			doc = next
		}
		json.NewEncoder(w).Encode(doc)
	}))
	defer server.Close()
	client, err := kubernetes.NewClient(server.URL, server.Client(), 20, 50)
	if err != nil {
		t.Fatal(err)
	}
	mirror := &faultMirror{}
	gvr := kubernetes.GVR{Version: "v1", Resource: "pods"}
	warm := func(instance string) *Graph {
		g := New("tenant-a", "cluster-a", instance, []kubernetes.GVR{gvr})
		if err := g.Replace(context.Background(), 0, kubernetes.Snapshot{GVR: gvr, Objects: []unstructured.Unstructured{object("Pod", "apps", "web", "pod-a", nil)}, State: kubernetes.GVRState{LastListCompletedAt: time.Now(), LastConnectivityProbeAt: time.Now(), WatchConnected: true, WatchContinuous: true}}); err != nil {
			t.Fatal(err)
		}
		return g
	}
	active := warm("pod-process-1")
	first := &Lease{Client: client, Graph: active, Mirror: mirror, Namespace: "owned", Name: "graph", Endpoint: "https://127.0.0.1:9444"}
	ctx := context.Background()
	if err := first.Tick(ctx); err != nil || active.OwnerEpoch() != 1 {
		t.Fatalf("acquire %v epoch=%d", err, active.OwnerEpoch())
	}
	// A database route cannot elect a new process, even when the route expired.
	mirror.mu.Lock()
	mirror.value.ExpiresAt = time.Now().Add(-time.Hour)
	mirror.mu.Unlock()
	restart := warm("same-pod-new-process")
	second := &Lease{Client: client, Graph: restart, Mirror: mirror, Namespace: "owned", Name: "graph", Endpoint: "https://127.0.0.1:9444"}
	if err := second.Tick(ctx); err == nil || restart.OwnerEpoch() != 0 {
		t.Fatal("DB mirror elected pod restart")
	}
	mu.Lock()
	doc.Spec.RenewTime = metav1.NewMicroTime(time.Now().Add(-time.Minute))
	mu.Unlock()
	unsynced := New("tenant-a", "cluster-a", "standby-unsynced", []kubernetes.GVR{gvr})
	third := &Lease{Client: client, Graph: unsynced, Mirror: mirror, Namespace: "owned", Name: "graph", Endpoint: "https://127.0.0.1:9444"}
	if err := third.Tick(ctx); err == nil {
		t.Fatal("unsynced standby became active")
	}
	if err := second.Tick(ctx); err != nil || restart.OwnerEpoch() != 2 {
		t.Fatalf("restart takeover %v epoch=%d", err, restart.OwnerEpoch())
	}
	if err := first.Confirm(ctx, 1); err == nil {
		t.Fatal("old active survived fencing")
	}
	mu.Lock()
	status = 503
	mu.Unlock()
	if err := second.Tick(ctx); err == nil {
		t.Fatal("API partition retained lease")
	}
	restart.mu.RLock()
	deadline := restart.deadline
	restart.mu.RUnlock()
	if !deadline.IsZero() {
		t.Fatal("partition retained query deadline")
	}
	mu.Lock()
	status = 404
	mu.Unlock()
	if err := second.Tick(ctx); err == nil {
		t.Fatal("deleted Lease elected owner")
	}
	mu.Lock()
	status = 200
	doc.Metadata.UID = "recreated"
	doc.Metadata.Annotations["ops.platform/owner-epoch"] = "0"
	mu.Unlock()
	if err := second.Tick(ctx); err == nil {
		t.Fatal("recreated Lease reset epoch")
	}
}

func TestQualifiedFirstOwnerAcquisitionFailureIsObservable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer server.Close()
	client, err := kubernetes.NewClient(server.URL, server.Client(), 20, 50)
	if err != nil {
		t.Fatal(err)
	}
	gvr := kubernetes.GVR{Version: "v1", Resource: "pods"}
	g := New("tenant-a", "cluster-a", "first-owner", []kubernetes.GVR{gvr})
	if err = g.Replace(t.Context(), 0, kubernetes.Snapshot{GVR: gvr, Objects: []unstructured.Unstructured{object("Pod", "apps", "web", "pod-a", nil)}, State: kubernetes.GVRState{LastListCompletedAt: time.Now(), LastConnectivityProbeAt: time.Now(), WatchConnected: true, WatchContinuous: true}}); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	l := &Lease{Client: client, Graph: g, Mirror: &faultMirror{}, Namespace: "owned", Name: "graph", Endpoint: "https://127.0.0.1:9444", Logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	if l.Tick(t.Context()) == nil {
		t.Fatal("forbidden native Lease read accepted")
	}
	if !strings.Contains(logs.String(), `"stage":"lease_read"`) {
		t.Fatal("first qualified owner failure was silent")
	}
	if strings.Contains(logs.String(), "tenant-a") || strings.Contains(logs.String(), "127.0.0.1") {
		t.Fatal("ownership diagnostics leaked routing facts")
	}
}

// Linux clocks retain nanoseconds; Darwin's clock precision masked this native
// API contract failure in the host wire test. Decode with the locked upstream
// Lease type rather than our own DTO so the fake cannot accept invalid JSON.
func TestLeaseWireUsesNativeMicroTimeWithNanosecondClock(t *testing.T) {
	doc := LeaseDocument{APIVersion: "coordination.k8s.io/v1", Kind: "Lease"}
	doc.Metadata.Name, doc.Metadata.Namespace = "graph", "owned"
	doc.Spec.RenewTime = metav1.NewMicroTime(time.Date(2026, 10, 2, 8, 57, 45, 896794703, time.UTC))
	doc.Spec.AcquireTime = doc.Spec.RenewTime
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var native struct {
		Spec struct{ RenewTime, AcquireTime *metav1.MicroTime }
	}
	if err = json.Unmarshal(raw, &native); err != nil {
		t.Fatalf("actual native Lease decoder rejects nanosecond clock wire: %v", err)
	}
	if native.Spec.RenewTime == nil || native.Spec.AcquireTime == nil || native.Spec.RenewTime.Nanosecond() != 896794000 {
		t.Fatal("Lease timestamp did not preserve native microsecond precision")
	}
}

func TestCancelledLeaseConfirmationDoesNotInvalidateOtherReaders(t *testing.T) {
	doc := LeaseDocument{APIVersion: "coordination.k8s.io/v1", Kind: "Lease"}
	doc.Metadata.UID = "native"
	doc.Metadata.Annotations = map[string]string{"ops.platform/owner-epoch": "1"}
	doc.Spec.HolderIdentity = "instance"
	doc.Spec.LeaseDurationSeconds = 15
	doc.Spec.RenewTime = metav1.NewMicroTime(time.Now())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(doc) }))
	defer server.Close()
	client, err := kubernetes.NewClient(server.URL, server.Client(), 20, 50)
	if err != nil {
		t.Fatal(err)
	}
	g := New("tenant-a", "cluster-a", "instance", nil)
	g.SetOwner(1, time.Now().Add(8*time.Second))
	lease := &Lease{Client: client, Graph: g, Namespace: "owned", Name: "graph", leaseUID: "native", epoch: 1, observed: true}
	g.mu.RLock()
	deadline := g.deadline
	g.mu.RUnlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = lease.Confirm(ctx, 1); err == nil {
		t.Fatal("cancelled read accepted")
	}
	g.mu.RLock()
	after := g.deadline
	g.mu.RUnlock()
	if !after.Equal(deadline) {
		t.Fatal("caller cancellation invalidated independent native ownership")
	}
	if err = lease.Confirm(context.Background(), 1); err != nil {
		t.Fatalf("next reader failed valid native Lease: %v", err)
	}
	doc.Spec.HolderIdentity = "other"
	if err = lease.Confirm(context.Background(), 1); err == nil {
		t.Fatal("actual owner change accepted")
	}
	g.mu.RLock()
	after = g.deadline
	g.mu.RUnlock()
	if !after.IsZero() {
		t.Fatal("actual owner change failed to invalidate")
	}
}

type cancelLeaseRead struct {
	io.Reader
	cancel context.CancelFunc
}

func (r cancelLeaseRead) Read(b []byte) (int, error) {
	n, err := r.Reader.Read(b)
	r.cancel()
	return n, err
}
func (r cancelLeaseRead) Close() error { return nil }

type leaseResponseTransport struct {
	payload []byte
	cancel  context.CancelFunc
}

func (t leaseResponseTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: cancelLeaseRead{bytes.NewReader(t.payload), t.cancel}}, nil
}
func TestLeaseResponseCancellationStillFencesKnownOwnershipChange(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(strconv.FormatBool(changed), func(t *testing.T) {
			doc := LeaseDocument{APIVersion: "coordination.k8s.io/v1", Kind: "Lease"}
			doc.Metadata.UID = "native"
			doc.Metadata.Annotations = map[string]string{"ops.platform/owner-epoch": "1"}
			doc.Spec.HolderIdentity = "instance"
			if changed {
				doc.Spec.HolderIdentity = "other"
			}
			doc.Spec.LeaseDurationSeconds = 15
			doc.Spec.RenewTime = metav1.NewMicroTime(time.Now())
			payload, _ := json.Marshal(doc)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client, err := kubernetes.NewClient("http://lease.test", &http.Client{Transport: leaseResponseTransport{payload, cancel}}, 20, 50)
			if err != nil {
				t.Fatal(err)
			}
			g := New("tenant-a", "cluster-a", "instance", nil)
			g.SetOwner(1, time.Now().Add(8*time.Second))
			lease := &Lease{Client: client, Graph: g, Namespace: "owned", Name: "graph", leaseUID: "native", epoch: 1, observed: true}
			if lease.Confirm(ctx, 1) == nil {
				t.Fatal("cancelled response accepted")
			}
			g.mu.RLock()
			deadline := g.deadline
			g.mu.RUnlock()
			if changed != deadline.IsZero() {
				t.Fatalf("known ownership change=%v revoked=%v", changed, deadline.IsZero())
			}
		})
	}
}

// One flaky read of a loaded API server is not evidence that ownership
// changed: the read fails closed, and a following confirm of the same still
// valid lease must succeed instead of cascading degraded in-flight queries.
type flakyLeaseTransport struct {
	payload []byte
	failed  *bool
}

func (t flakyLeaseTransport) RoundTrip(*http.Request) (*http.Response, error) {
	if !*t.failed {
		*t.failed = true
		return nil, errors.New("get \"https://api.test\": connection reset by peer")
	}
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(t.payload))}, nil
}

func TestLeaseConfirmTransportFailureKeepsConfirmedOwnership(t *testing.T) {
	doc := LeaseDocument{APIVersion: "coordination.k8s.io/v1", Kind: "Lease"}
	doc.Metadata.UID = "native"
	doc.Metadata.Annotations = map[string]string{"ops.platform/owner-epoch": "1"}
	doc.Spec.HolderIdentity = "instance"
	doc.Spec.LeaseDurationSeconds = 15
	doc.Spec.RenewTime = metav1.NewMicroTime(time.Now())
	payload, _ := json.Marshal(doc)
	failed := false
	client, err := kubernetes.NewClient("http://lease.test", &http.Client{Transport: flakyLeaseTransport{payload, &failed}}, 20, 50)
	if err != nil {
		t.Fatal(err)
	}
	g := New("tenant-a", "cluster-a", "instance", nil)
	g.SetOwner(1, time.Now().Add(8*time.Second))
	lease := &Lease{Client: client, Graph: g, Namespace: "owned", Name: "graph", leaseUID: "native", epoch: 1, observed: true}
	if err := lease.Confirm(context.Background(), 1); err == nil {
		t.Fatal("transport failure accepted as ownership proof")
	}
	g.mu.RLock()
	deadline := g.deadline
	g.mu.RUnlock()
	if deadline.IsZero() {
		t.Fatal("one flaky lease read destroyed confirmed ownership")
	}
	if err := lease.Confirm(context.Background(), 1); err != nil {
		t.Fatalf("confirmed ownership stopped confirming after a flaky read: %v", err)
	}
}

// The renewal loop holds the fence across one API-server round trip. A request
// path used to inherit that latency: during the 2026-10-18 page run a follow-up
// get_assistant_context was killed at 3007ms while the owner was renewing. A
// fenced request must fail closed instead of blocking behind the renewal.
func TestConfirmDoesNotInheritRenewalLatency(t *testing.T) {
	doc := LeaseDocument{APIVersion: "coordination.k8s.io/v1", Kind: "Lease"}
	doc.Metadata.Name, doc.Metadata.Namespace = "graph", "owned"
	doc.Metadata.UID = "native"
	doc.Metadata.ResourceVersion = "2"
	doc.Metadata.Annotations = map[string]string{"ops.platform/owner-epoch": "1"}
	doc.Spec.HolderIdentity = "instance"
	doc.Spec.LeaseDurationSeconds = 15
	doc.Spec.RenewTime = metav1.NewMicroTime(time.Now())
	payload, _ := json.Marshal(doc)
	var first sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			first.Do(func() { time.Sleep(1200 * time.Millisecond) })
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(payload)
	}))
	defer server.Close()
	client, err := kubernetes.NewClient(server.URL, server.Client(), 20, 50)
	if err != nil {
		t.Fatal(err)
	}
	g := New("tenant-a", "cluster-a", "instance", nil)
	g.SetOwner(1, time.Now().Add(8*time.Second))
	lease := &Lease{Client: client, Graph: g, Mirror: &faultMirror{}, Namespace: "owned", Name: "graph", Endpoint: "https://127.0.0.1:9444", leaseUID: "native", epoch: 1, observed: true}
	renewed := make(chan error, 1)
	go func() { renewed <- lease.Tick(context.Background()) }()
	time.Sleep(100 * time.Millisecond)
	started := time.Now()
	err = lease.Confirm(context.Background(), 1)
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("request path accepted ownership proof without reading the native Lease")
	}
	if elapsed > 700*time.Millisecond {
		t.Fatalf("request path blocked %s behind one renewal", elapsed)
	}
	if tickErr := <-renewed; tickErr != nil {
		t.Fatalf("renewal itself failed: %v", tickErr)
	}
}
