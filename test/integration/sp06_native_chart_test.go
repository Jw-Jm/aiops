package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"ops-platform/internal/investigation"
)

// The caller installs the authenticated shipped images/Chart and uses a real
// Keycloak bearer. This gate does not replace the model with a test provider.
func sp06NativeInvestigation(t *testing.T, ctx context.Context, db *sql.DB, namespace, incidentID string, call func(string, string, []byte) (int, []byte)) {
	t.Helper()
	var revision int64
	if err := db.QueryRowContext(ctx, `SELECT revision FROM incident.records WHERE incident_id=$1`, incidentID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"triggerKind": "manual", "triggerRevision": revision})
	status, raw := call("POST", "/api/v1/incidents/"+incidentID+"/investigations", body)
	if status != 202 {
		t.Fatalf("native investigation creation: %d %s", status, raw)
	}
	var response struct{ Data investigation.Job }
	if json.Unmarshal(raw, &response) != nil || response.Data.SchemaVersion != "investigation-job/v2" {
		t.Fatalf("native Job contract: %s", raw)
	}
	job := response.Data
	path := "/api/v1/investigations/" + job.JobID.String()
	for deadline := time.Now().Add(4 * time.Minute); ; {
		status, raw = call("GET", path, nil)
		if status != 200 || json.Unmarshal(raw, &response) != nil {
			t.Fatalf("native Job read: %d %s", status, raw)
		}
		job = response.Data
		if job.State != "queued" && job.State != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("native resident dispatcher did not finish: %s", raw)
		}
		time.Sleep(time.Second)
	}
	if job.State != "succeeded" && job.State != "partial" {
		t.Fatalf("real native Holmes/model investigation failed: %s", raw)
	}
	if job.Consumed.ToolCalls < 1 || job.Consumed.ModelRequests < 1 || job.Reserved != (investigation.Usage{}) || len(job.Result) == 0 {
		t.Fatalf("native model/MCP/Go validation ledger incomplete: %s", raw)
	}
	var tools, models, prohibited int
	err := db.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE tool_name<>'model' AND state='succeeded'),count(*) FILTER(WHERE tool_name='model' AND state='succeeded'),count(*) FILTER(WHERE tool_name IN('execute_command','ssh','sql','fetch_url')) FROM investigation.steps WHERE job_id=$1`, job.JobID).Scan(&tools, &models, &prohibited)
	if err != nil || tools < 1 || models < 1 || prohibited != 0 {
		t.Fatalf("native persisted Step ledger: tools=%d models=%d prohibited=%d err=%v", tools, models, prohibited, err)
	}
	var audits int64
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM audit.records WHERE entity_id=$1 AND event_type LIKE 'investigation.%'`, job.JobID).Scan(&audits); err != nil || audits != job.EventSeq {
		t.Fatalf("native Ledger/Audit atomicity: events=%d audits=%d err=%v", job.EventSeq, audits, err)
	}
	var capabilities struct {
		RequestID string `json:"requestId"`
		Data      struct {
			APIReady      bool `json:"apiReady"`
			GraphReady    bool `json:"graphReady"`
			Investigation struct {
				Status   string `json:"status"`
				ReadOnly bool   `json:"readOnly"`
			} `json:"investigation"`
		} `json:"data"`
	}
	// A completed model call is not a Graph connectivity probe. Observe a
	// currently fresh authorized snapshot after normal watch reconnection,
	// preserving the same native source-readiness deadline used by the caller.
	for deadline := time.Now().Add(90 * time.Second); ; {
		status, raw = call("GET", "/api/v1/capabilities?clusterUid="+job.Scope.Cluster, nil)
		if status != 200 || json.Unmarshal(raw, &capabilities) != nil || capabilities.Data.GraphReady || time.Now().After(deadline) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if status != 200 || json.Unmarshal(raw, &capabilities) != nil || capabilities.RequestID == "" || !capabilities.Data.APIReady || !capabilities.Data.GraphReady || !capabilities.Data.Investigation.ReadOnly || capabilities.Data.Investigation.Status != "unverified" {
		t.Fatalf("native currently authorized capabilities: %d %s", status, raw)
	}
	status, raw = call("GET", path+"/events", nil)
	if status != 200 {
		t.Fatalf("native durable SSE: %d %s", status, raw)
	}
	var last int64
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event struct {
			EventID string `json:"eventId"`
			JobID   string `json:"jobId"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) != nil {
			t.Fatal("native SSE malformed")
		}
		n, err := strconv.ParseInt(event.EventID, 10, 64)
		if err != nil || n <= last || event.JobID != job.JobID.String() {
			t.Fatal("native SSE ordering/identity invalid")
		}
		last = n
	}
	if last != job.EventSeq {
		t.Fatalf("native persisted SSE gap: last=%d ledger=%d", last, job.EventSeq)
	}
	t.Log(fmt.Sprintf("shipped SP06 Chart/API/Worker/investigator -> actual llama3.1:8b-16k -> standard MCP -> native Incident/Evidence -> fenced Ledger/Go validator -> durable SSE: namespace=%s job=%s state=%s tools=%d models=%d events=%d", namespace, job.JobID, job.State, tools, models, last))
}

// Ten real resident investigations run beside the shipped collection, archive,
// graph and deterministic RCA paths. Each Incident comes from an actual owned
// native Pod observation, never a direct Job/Incident database insertion.
func sp06NativeConcurrentInvestigations(t *testing.T, ctx context.Context, db *sql.DB, namespace, tenant, cluster, resourcePath, archivedEvidenceID, originalIncident string, create func(any) []byte, call func(string, string, []byte) (int, []byte)) {
	t.Helper()
	ids := make([]string, 0, 10)
	for n := range 10 {
		raw := create(map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": fmt.Sprintf("sp06-concurrent-%d", n), "namespace": namespace}, "spec": map[string]any{"automountServiceAccountToken": false, "restartPolicy": "Never", "containers": []any{map[string]any{"name": "never-pulled", "image": "fixture.invalid/sp06-concurrency:missing", "imagePullPolicy": "Never"}}}})
		var pod struct{ Metadata struct{ UID string } }
		if json.Unmarshal(raw, &pod) != nil || pod.Metadata.UID == "" {
			t.Fatal("owned concurrency Pod identity missing")
		}
		ids = append(ids, "k8s+v1://"+tenant+"/"+cluster+"/core/Pod/"+pod.Metadata.UID)
	}
	incidents := make([]string, 10)
	for deadline := time.Now().Add(90 * time.Second); ; {
		ready := 0
		for n, id := range ids {
			if db.QueryRowContext(ctx, `SELECT COALESCE(min(l.incident_id::text),'') FROM finding.records f JOIN incident.finding_links l USING(tenant_id,finding_id) JOIN finding.evidence_refs r USING(tenant_id,finding_id) JOIN platform.evidence_metadata m ON m.tenant_id=r.tenant_id AND m.evidence_id=r.evidence_id AND m.replay_state='archived_verified' WHERE f.tenant_id=$1 AND f.resource_canonical_id=$2 AND f.payload->>'ruleId'='k8sgpt/PodRuntimeFault/v1' AND f.lifecycle_state='firing'`, tenant, id).Scan(&incidents[n]) != nil {
				t.Fatal("native concurrent Incident read")
			}
			if incidents[n] != "" {
				ready++
			}
		}
		if ready == 10 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("native collector/Analyzer/archive produced only %d/10 Incidents", ready)
		}
		time.Sleep(250 * time.Millisecond)
	}
	jobs := make([]investigation.Job, 10)
	for n, id := range incidents {
		status, raw := call("POST", "/api/v1/incidents/"+id+"/investigations", []byte(`{}`))
		var envelope struct{ Data investigation.Job }
		if status != 202 || json.Unmarshal(raw, &envelope) != nil {
			t.Fatalf("native concurrent create %d %s", status, raw)
		}
		jobs[n] = envelope.Data
		for previous := 0; previous < n; previous++ {
			if jobs[previous].JobID == jobs[n].JobID {
				t.Fatal("distinct native Incidents collapsed into one Job")
			}
		}
	}
	mainPaths := []string{resourcePath, "/api/v1/findings?clusterUid=" + cluster, "/api/v1/incidents/" + originalIncident, "/api/v1/evidence/" + archivedEvidenceID, "/api/v1/incidents/" + originalIncident + "/rca"}
	peak := 0
	for deadline := time.Now().Add(3 * time.Minute); ; {
		running, terminal := 0, 0
		for n := range jobs {
			status, raw := call("GET", "/api/v1/investigations/"+jobs[n].JobID.String(), nil)
			var envelope struct{ Data investigation.Job }
			if status != 200 || json.Unmarshal(raw, &envelope) != nil {
				t.Fatalf("native concurrent read %d %s", status, raw)
			}
			jobs[n] = envelope.Data
			if jobs[n].State == "running" {
				running++
			} else if jobs[n].State != "queued" {
				terminal++
			}
		}
		if running > peak {
			peak = running
		}
		for _, path := range mainPaths {
			if status, raw := call("GET", path, nil); status != 200 {
				t.Fatalf("mainline during ten native investigations %s: %d %s", path, status, raw)
			}
		}
		if terminal == 10 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("native concurrent investigation settlement incomplete: %d/10", terminal)
		}
		time.Sleep(250 * time.Millisecond)
	}
	if peak != 10 {
		t.Fatalf("ten native investigations were not simultaneous: peak=%d", peak)
	}
	outcomes := map[string]int{}
	for _, job := range jobs {
		if job.Consumed.ModelRequests < 1 || job.Reserved != (investigation.Usage{}) || !job.Consumed.Within(job.Budget.Usage) {
			t.Fatalf("native concurrent resident/model/budget admission incomplete: %+v", job)
		}
		var audits int64
		if db.QueryRowContext(ctx, `SELECT count(*) FROM audit.records WHERE entity_id=$1 AND event_type LIKE 'investigation.%'`, job.JobID).Scan(&audits) != nil || audits != job.EventSeq {
			t.Fatal("native concurrent Ledger/Audit atomicity")
		}
		outcomes[job.State]++
	}
	t.Logf("ten actual native resident/model investigations peak=%d terminal=%v; current Graph/Finding/Incident/archived Evidence/deterministic RCA HTTP paths stayed available; no performance or accuracy acceptance", peak, outcomes)
}

// Withdraw only this owned investigator's model route. The shared model and all
// mainline services remain running; the production adapter observes a real
// network timeout rather than a mock provider response.
func sp06NativeModelWithdrawal(t *testing.T, ctx context.Context, db *sql.DB, namespace, tenant, cluster, resourcePath, evidenceID, originalIncident string, run func(string, ...string) []byte, create func(any) []byte, call func(string, string, []byte) (int, []byte)) {
	t.Helper()
	raw := run("kubectl", "--context", "orbstack", "-n", namespace, "get", "networkpolicy", "ops-sp06-investigator", "-o", "json")
	var original map[string]any
	if json.Unmarshal(raw, &original) != nil {
		t.Fatal("owned investigator policy missing")
	}
	var blocked map[string]any
	if json.Unmarshal(raw, &blocked) != nil {
		t.Fatal("owned investigator policy decode")
	}
	spec := blocked["spec"].(map[string]any)
	egress := []any{}
	for _, rule := range spec["egress"].([]any) {
		model := false
		for _, port := range rule.(map[string]any)["ports"].([]any) {
			if port.(map[string]any)["port"] == float64(11434) {
				model = true
			}
		}
		if !model {
			egress = append(egress, rule)
		}
	}
	if len(egress) != 2 {
		t.Fatal("unexpected owned investigator model route")
	}
	spec["egress"] = egress
	patch := func(value any) {
		body, err := json.Marshal(map[string]any{"spec": value})
		if err != nil {
			t.Fatal(err)
		}
		run("kubectl", "--context", "orbstack", "-n", namespace, "patch", "networkpolicy", "ops-sp06-investigator", "--type=merge", "-p", string(body))
	}
	patch(spec)
	defer func() {
		patch(original["spec"])
	}()
	// Restart this owned resident to close pooled model connections before the
	// fault. Existing conntrack entries must not turn withdrawal into a test of
	// an already established socket. Durable Jobs/Steps stay in PostgreSQL.
	run("kubectl", "--context", "orbstack", "-n", namespace, "rollout", "restart", "deployment/ops-investigator")
	run("kubectl", "--context", "orbstack", "-n", namespace, "rollout", "status", "deployment/ops-investigator", "--timeout=90s")
	// Allow the CNI's policy update to converge before a new model connection.
	time.Sleep(2 * time.Second)
	raw = create(map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "sp06-model-withdrawal", "namespace": namespace}, "spec": map[string]any{"automountServiceAccountToken": false, "restartPolicy": "Never", "containers": []any{map[string]any{"name": "never-pulled", "image": "fixture.invalid/sp06-withdrawal:missing", "imagePullPolicy": "Never"}}}})
	var pod struct{ Metadata struct{ UID string } }
	if json.Unmarshal(raw, &pod) != nil || pod.Metadata.UID == "" {
		t.Fatal("owned withdrawal Pod missing")
	}
	canonical := "k8s+v1://" + tenant + "/" + cluster + "/core/Pod/" + pod.Metadata.UID
	var incident string
	for deadline := time.Now().Add(90 * time.Second); ; {
		if db.QueryRowContext(ctx, `SELECT COALESCE(min(l.incident_id::text),'') FROM finding.records f JOIN incident.finding_links l USING(tenant_id,finding_id) WHERE f.tenant_id=$1 AND f.resource_canonical_id=$2 AND f.lifecycle_state='firing'`, tenant, canonical).Scan(&incident) != nil {
			t.Fatal("withdrawal Incident read")
		}
		if incident != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("mainline did not produce Incident during model withdrawal")
		}
		time.Sleep(250 * time.Millisecond)
	}
	status, raw := call("POST", "/api/v1/incidents/"+incident+"/investigations", []byte(`{}`))
	var envelope struct{ Data investigation.Job }
	if status != 202 || json.Unmarshal(raw, &envelope) != nil {
		t.Fatalf("withdrawal Job create: %d %s", status, raw)
	}
	jobID := envelope.Data.JobID.String()
	mainPaths := []string{resourcePath, "/api/v1/findings?clusterUid=" + cluster, "/api/v1/incidents/" + originalIncident, "/api/v1/evidence/" + evidenceID, "/api/v1/incidents/" + originalIncident + "/rca"}
	for deadline := time.Now().Add(2 * time.Minute); ; {
		status, raw = call("GET", "/api/v1/investigations/"+jobID, nil)
		if status != 200 || json.Unmarshal(raw, &envelope) != nil {
			t.Fatalf("withdrawal Job read: %d %s", status, raw)
		}
		for _, path := range mainPaths {
			if status, body := call("GET", path, nil); status != 200 {
				t.Fatalf("mainline with model unavailable %s: %d %s", path, status, body)
			}
		}
		if envelope.Data.State != "queued" && envelope.Data.State != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("unavailable model did not settle")
		}
		time.Sleep(250 * time.Millisecond)
	}
	if envelope.Data.State != "failed" || envelope.Data.Consumed.ModelRequests < 1 || envelope.Data.Reserved != (investigation.Usage{}) {
		t.Fatalf("model withdrawal was not honestly failed/settled: %s", raw)
	}
	status, raw = call("GET", "/api/v1/capabilities?clusterUid="+cluster, nil)
	var caps struct {
		Data struct {
			APIReady      bool `json:"apiReady"`
			Investigation struct {
				Status string `json:"status"`
			} `json:"investigation"`
		} `json:"data"`
	}
	if status != 200 || json.Unmarshal(raw, &caps) != nil || !caps.Data.APIReady || caps.Data.Investigation.Status != "degraded" {
		t.Fatalf("unavailable model capabilities: %d %s", status, raw)
	}
	t.Logf("owned CNI model-route withdrawal: actual resident model call failed honestly; Graph/Finding/Incident/archived Evidence/RCA stayed available; job=%s", jobID)
}
