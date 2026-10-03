package integration

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"net/http"
	"ops-platform/internal/app"
	"ops-platform/internal/finding"
	"ops-platform/internal/incident"
	kube "ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/resourcestore"
	"os"
	"strings"
	"testing"
	"time"
)

type sp05OfficialFaultTransport struct {
	base        http.RoundTripper
	probeStatus int
	reads       int
	metricMode  string
}

func (tr *sp05OfficialFaultTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	tr.reads++
	if r.Method != "GET" {
		return nil, io.ErrUnexpectedEOF
	}
	if r.URL.Path == "/version" && tr.probeStatus != 0 {
		return &http.Response{StatusCode: tr.probeStatus, Body: io.NopCloser(bytes.NewBufferString(`{"kind":"Status"}`)), Header: http.Header{}, Request: r}, nil
	}
	if strings.HasPrefix(r.URL.Path, "/apis/metrics.k8s.io/v1beta1/nodes/") && tr.metricMode != "" {
		name := strings.TrimPrefix(r.URL.Path, "/apis/metrics.k8s.io/v1beta1/nodes/")
		cpu, memory := "100000", "1Pi"
		if tr.metricMode == "healthy" {
			cpu, memory = "0", "0"
		}
		raw, _ := json.Marshal(map[string]any{"apiVersion": "metrics.k8s.io/v1beta1", "kind": "NodeMetrics", "metadata": map[string]any{"name": name}, "timestamp": time.Now().UTC(), "window": "30s", "usage": map[string]string{"cpu": cpu, "memory": memory}})
		if tr.metricMode == "drift" {
			raw = []byte(`{"apiVersion":"metrics.k8s.io/v2","kind":"NodeMetrics"}`)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw)), Header: http.Header{}, Request: r}, nil
	}
	return tr.base.RoundTrip(r)
}

func TestSP05OfficialObservationsActualOrbStackAndControlledProbeFailure(t *testing.T) {
	tenant, source := uuid.New(), uuid.New()
	collector, ns := sp04OwnedKubernetes(t, t.Context(), tenant.String(), source.String())
	ctx, db, pool, b := sp05Database(t, sp05Seed{Tenant: tenant, Source: source, ClusterUID: collector.ClusterUID, Namespace: ns, Backend: collector.BackendLogicalID})
	archives := sp05GoldenArchive(t, ctx, pool, b.TenantID)
	ca, err := os.ReadFile(collector.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	token, err := os.ReadFile(collector.TokenFile)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("native CA invalid")
	}
	base := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
	defer base.CloseIdleConnections()
	fault := &sp05OfficialFaultTransport{base: nativeLeaseContractTransport{base, strings.TrimSpace(string(token)), t}}
	client, err := kube.NewClient(collector.Endpoint, &http.Client{Transport: fault, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, 10, 25)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(ctx, "GET", "/api/v1/nodes", nil)
	if err != nil {
		t.Fatal(err)
	}
	var nativeNodes struct {
		Items []map[string]any `json:"items"`
	}
	var nodes struct{ Items []unstructured.Unstructured }
	err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&nativeNodes)
	for _, object := range nativeNodes.Items {
		o := unstructured.Unstructured{Object: object}
		o.SetKind("Node")
		o.SetAPIVersion("v1")
		nodes.Items = append(nodes.Items, o)
	}
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || len(nodes.Items) == 0 {
		t.Fatalf("actual native Nodes unavailable: %v", err)
	}
	snapshot := kube.Snapshot{GVR: kube.GVR{Version: "v1", Resource: "nodes"}, Objects: nodes.Items, State: kube.GVRState{LastListCompletedAt: time.Now()}}
	repo := resourcestore.Repository{Pool: pool, ExpectedRevision: 1, BackendLogicalID: collector.BackendLogicalID}
	filtered, err := repo.FilterSnapshot(ctx, b.TenantID.String(), b.SourceID.String(), collector.ClusterUID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SyncSnapshot(ctx, b.TenantID.String(), b.SourceID.String(), collector.ClusterUID, "", "core", filtered, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	report, err := app.InspectSP05OfficialObservations(ctx, archives, collector, client, 0)
	if err != nil || !report.ProbeAvailable {
		t.Fatalf("native available control plane misclassified: %+v %v", report, err)
	}
	if !report.MetricAvailable && len(report.DegradedSources) == 0 {
		t.Fatal("actual missing/unauthorized Metrics silently claimed complete")
	}
	t.Logf("actual native official read report: %+v (Metrics availability is observed, never a performance measurement)", report)
	fault.probeStatus = 503
	report, err = app.InspectSP05OfficialObservations(ctx, archives, collector, client, 0)
	if err != nil || report.ProbeAvailable || report.Candidates != 1 {
		t.Fatalf("controlled collector-side 503 lost native Node symptom: %+v %v", report, err)
	}
	if err := (finding.Service{Pool: pool}).RelayPass(ctx, b.TenantID, incident.Consume, 10); err != nil {
		t.Fatal(err)
	}
	var active int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM finding.records WHERE tenant_id=$1 AND payload->>'normalizedSymptom'='ControlPlaneUnreachable' AND lifecycle_state='firing'`, b.TenantID).Scan(&active); err != nil || active != 1 {
		t.Fatalf("actual ingestion/relay lost control-plane Finding: count=%d %v", active, err)
	}
	fault.probeStatus = 403
	report, err = app.InspectSP05OfficialObservations(ctx, archives, collector, client, 0)
	if err != nil || report.Candidates != 0 || report.ProbeAvailable || len(report.DegradedSources) == 0 {
		t.Fatalf("403 became outage or recovery: %+v %v", report, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM finding.records WHERE tenant_id=$1 AND payload->>'normalizedSymptom'='ControlPlaneUnreachable' AND lifecycle_state='firing'`, b.TenantID).Scan(&active); err != nil || active != 1 {
		t.Fatal("authorization failure resolved prior outage")
	}
	fault.probeStatus = 0
	if _, err := app.InspectSP05OfficialObservations(ctx, archives, collector, client, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM finding.records WHERE tenant_id=$1 AND payload->>'normalizedSymptom'='ControlPlaneUnreachable' AND lifecycle_state='resolved'`, b.TenantID).Scan(&active); err != nil || active != 1 {
		t.Fatalf("actual native /version recovery did not resolve symptom: %d %v", active, err)
	}
	// Native Metrics API is absent on this cluster. Inject only its versioned
	// response transport, while reading both Node observations and scope from
	// the real API; outcomes still use the production ingestion/relay/archive.
	fault.metricMode = "high"
	report, err = app.InspectSP05OfficialObservations(ctx, archives, collector, client, 0)
	if err != nil || !report.MetricAvailable || report.Candidates != 3 {
		t.Fatalf("positive protocol Metrics did not enter unified ingestion: %+v %v", report, err)
	}
	if err = (finding.Service{Pool: pool}).RelayPass(ctx, b.TenantID, incident.Consume, 10); err != nil {
		t.Fatal(err)
	}
	var metricActive int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM finding.records WHERE tenant_id=$1 AND payload->>'ruleId' IN ('kubernetes/CPUUtilizationHigh/v1','kubernetes/MemoryUtilizationHigh/v1') AND lifecycle_state='firing'`, b.TenantID).Scan(&metricActive); err != nil || metricActive != 2 {
		t.Fatalf("actual metric Finding count=%d %v", metricActive, err)
	}
	fault.metricMode = "drift"
	report, err = app.InspectSP05OfficialObservations(ctx, archives, collector, client, 0)
	if err != nil || report.MetricAvailable || len(report.DegradedSources) == 0 {
		t.Fatalf("Metrics schema drift claimed complete: %+v %v", report, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM finding.records WHERE tenant_id=$1 AND payload->>'ruleId' IN ('kubernetes/CPUUtilizationHigh/v1','kubernetes/MemoryUtilizationHigh/v1') AND lifecycle_state='firing'`, b.TenantID).Scan(&metricActive); err != nil || metricActive != 2 {
		t.Fatal("schema drift resolved unavailable metric")
	}
	fault.metricMode = "healthy"
	report, err = app.InspectSP05OfficialObservations(ctx, archives, collector, client, 0)
	if err != nil || !report.MetricAvailable {
		t.Fatalf("positive protocol Metrics recovery unavailable: %+v %v", report, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM finding.records WHERE tenant_id=$1 AND payload->>'ruleId' IN ('kubernetes/CPUUtilizationHigh/v1','kubernetes/MemoryUtilizationHigh/v1') AND lifecycle_state='resolved'`, b.TenantID).Scan(&metricActive); err != nil || metricActive != 2 {
		t.Fatal("healthy native-format metric did not recover both symptoms")
	}
	if err = archives.MaintenancePass(ctx, nil); err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(ctx, `SELECT e.evidence_id::text FROM finding.evidence_refs f JOIN platform.evidence_metadata e USING(tenant_id,evidence_id) JOIN finding.records r USING(tenant_id,finding_id) WHERE e.tenant_id=$1 AND r.payload->>'ruleId' IN ('kubernetes/CPUUtilizationHigh/v1','kubernetes/MemoryUtilizationHigh/v1')`, b.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	metricRefs := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		metricRefs = append(metricRefs, id)
	}
	rows.Close()
	var wronglyTyped int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM finding.evidence_refs f JOIN platform.evidence_metadata e USING(tenant_id,evidence_id) JOIN finding.records r USING(tenant_id,finding_id) WHERE e.tenant_id=$1 AND r.payload->>'ruleId' IN ('kubernetes/CPUUtilizationHigh/v1','kubernetes/MemoryUtilizationHigh/v1') AND e.metadata->>'type'<>'metric'`, b.TenantID).Scan(&wronglyTyped); err != nil || wronglyTyped != 0 {
		t.Fatalf("official Metrics persisted as non-Metric Evidence: %d %v", wronglyTyped, err)
	}
	if len(metricRefs) < 2 {
		t.Fatal("Metric Evidence refs absent")
	}
	for _, id := range metricRefs {
		if _, _, err = archives.Read(ctx, b.TenantID, uuid.MustParse(id)); err != nil {
			t.Fatalf("actual immutable metric Evidence unreadable: %v", err)
		}
	}
	t.Log("positive versioned Metrics transport Fixture -> real Node UID/auth fence -> unified Finding -> real Incident and immutable Evidence; native Metrics-server positive capability remains unverified")
	if _, err := db.ExecContext(ctx, `UPDATE platform.source_registrations SET status='disabled',revision=revision+1 WHERE tenant_id=$1 AND source_id=$2`, b.TenantID, b.SourceID); err != nil {
		t.Fatal(err)
	}
	prior := fault.reads
	if _, err := app.InspectSP05OfficialObservations(ctx, archives, collector, client, 0); err == nil || prior != fault.reads {
		t.Fatalf("withdrawn source used native API: err=%v readsBefore=%d readsAfter=%d", err, prior, fault.reads)
	}
	t.Log("real OrbStack native Node/source -> official probe -> unified ingestion -> real PostgreSQL Incident and OpenBao/SeaweedFS archive; isolated HTTP 503/403 observation fault, real native recovery and source withdrawal; no shared API outage injected")
}
