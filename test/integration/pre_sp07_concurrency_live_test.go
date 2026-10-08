//go:build pre_sp07_live

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/investigation"
)

// Exercise the installed product, not a private application constructor. Ten
// API-created Jobs share the real model while the finding/incident API remains
// available. Mixed honest terminal failures are allowed; accuracy is not scored.
func TestFormalCurrentTenInvestigationsAndMainline(t *testing.T) {
	origin := os.Getenv("PRE_SP07_CURRENT_API_LOOPBACK")
	tokenPath := os.Getenv("PRE_SP07_CURRENT_TOKEN_FILE")
	incidentFile := os.Getenv("PRE_SP07_CURRENT_INCIDENT_IDS_FILE")
	cluster := os.Getenv("PRE_SP07_CURRENT_CLUSTER_UID")
	dsn := os.Getenv("PRE_SP07_CURRENT_READONLY_DATABASE_URL")
	output := os.Getenv("PRE_SP07_CURRENT_CONCURRENCY_RECEIPT")
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || dsn == "" || output == "" {
		t.Fatal("installed loopback API and private verification inputs required")
	}
	var incidents []string
	input, err := os.ReadFile(incidentFile)
	if err != nil || json.Unmarshal(input, &incidents) != nil || len(incidents) != 10 {
		t.Fatal("ten independent native current incidents required")
	}
	seenIncidents := map[string]bool{}
	for _, id := range incidents {
		if _, err := uuid.Parse(id); err != nil || seenIncidents[id] {
			t.Fatal("native incident identity invalid or repeated")
		}
		seenIncidents[id] = true
	}
	if _, err := uuid.Parse(cluster); err != nil {
		t.Fatal("actual registered native cluster UID required")
	}
	info, err := os.Stat(tokenPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		t.Fatal("private operator bearer required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 210*time.Second)
	defer cancel()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	call := func(method, path, key string) (int, []byte, error) {
		token, err := os.ReadFile(tokenPath)
		if err != nil {
			return 0, nil, err
		}
		var body io.Reader
		if method == http.MethodPost {
			body = bytes.NewBufferString("{}")
		}
		req, err := http.NewRequestWithContext(ctx, method, origin+path, body)
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		resp, err := client.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
		if err != nil || len(raw) > 2<<20 {
			return resp.StatusCode, nil, fmt.Errorf("bounded API read failed")
		}
		return resp.StatusCode, raw, nil
	}
	var jobs [10]investigation.Job
	peak, mainlineReads := 0, 0
	var simultaneousSamples []map[string]any
	complete := false
	receipt := map[string]any{"incidentIds": incidents, "clusterUid": cluster, "performance": "USER_WAIVED", "accuracyPass": false, "fullR0ToR6Pass": false}
	defer func() {
		// Persist metadata on failures too; never publish source facts or tokens.
		safe := []map[string]any{}
		for _, j := range jobs {
			safe = append(safe, map[string]any{"jobId": j.JobID, "state": j.State, "budget": j.Budget, "budgetConsumed": j.Consumed, "budgetReserved": j.Reserved, "eventSeq": j.EventSeq})
		}
		receipt["observedAt"] = time.Now().UTC()
		receipt["jobs"] = safe
		receipt["peakObservedRunning"] = peak
		receipt["simultaneousDatabaseSamples"] = simultaneousSamples
		receipt["mainlineReads"] = mainlineReads
		receipt["allTerminal"] = complete
		receipt["testPassed"] = !t.Failed()
		raw, _ := json.MarshalIndent(receipt, "", "  ")
		if os.WriteFile(output, append(raw, '\n'), 0644) != nil {
			t.Error("public concurrency metadata receipt unavailable")
		}
	}()
	mainline := func() {
		t.Helper()
		for _, path := range []string{"/api/v1/findings", "/api/v1/incidents"} {
			status, raw, err := call(http.MethodGet, path+"?clusterUid="+cluster+"&limit=200", "")
			var env struct {
				Data []json.RawMessage `json:"data"`
			}
			if err != nil || status != 200 || json.Unmarshal(raw, &env) != nil || len(env.Data) == 0 {
				t.Fatalf("real nonempty mainline unavailable: path=%s HTTP=%d", path, status)
			}
			mainlineReads++
		}
	}
	// Validate required listing inputs before creating durable Jobs.
	mainline()
	keys := [10]string{}
	errors := make(chan string, 10)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range jobs {
		keys[i] = uuid.NewString()
		wg.Go(func() {
			<-start
			status, raw, err := call(http.MethodPost, "/api/v1/incidents/"+incidents[i]+"/investigations", keys[i])
			var env struct {
				Data investigation.Job `json:"data"`
			}
			if err != nil || status != 202 || json.Unmarshal(raw, &env) != nil || env.Data.JobID == uuid.Nil {
				errors <- fmt.Sprintf("creation %d failed: HTTP %d", i, status)
				return
			}
			jobs[i] = env.Data
		})
	}
	close(start)
	wg.Wait()
	close(errors)
	for failure := range errors {
		t.Error(failure)
	}
	if t.Failed() {
		t.FailNow()
	}
	unique := map[uuid.UUID]bool{}
	jobIDs := make([]uuid.UUID, 0, 10)
	for i, job := range jobs {
		if unique[job.JobID] || job.IncidentID.String() != incidents[i] {
			t.Fatal("concurrent Job identity collided or changed incident")
		}
		unique[job.JobID] = true
		jobIDs = append(jobIDs, job.JobID)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal("read-only ledger verification unavailable")
	}
	defer conn.Close(context.Background())
	for ctx.Err() == nil {
		// A single database snapshot proves simultaneity; sequential API reads
		// alone could count Jobs that never overlapped.
		observation, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
		if err != nil {
			t.Fatal("read-only concurrency snapshot unavailable")
		}
		var running int
		var sampledAt time.Time
		_, err = observation.Exec(ctx, `SELECT set_config('app.tenant_id',$1,true)`, jobs[0].TenantID.String())
		if err == nil {
			err = observation.QueryRow(ctx, `SELECT count(*) FILTER(WHERE state='running'),statement_timestamp() FROM investigation.jobs WHERE tenant_id=$1 AND job_id=ANY($2)`, jobs[0].TenantID, jobIDs).Scan(&running, &sampledAt)
		}
		observation.Rollback(context.Background())
		if err != nil {
			t.Fatal("native simultaneous Job observation failed")
		}
		simultaneousSamples = append(simultaneousSamples, map[string]any{"observedAt": sampledAt, "running": running})
		terminal := 0
		for i := range jobs {
			status, raw, err := call(http.MethodGet, "/api/v1/investigations/"+jobs[i].JobID.String(), "")
			var env struct {
				Data investigation.Job `json:"data"`
			}
			if err != nil || status != 200 || json.Unmarshal(raw, &env) != nil || env.Data.JobID != jobs[i].JobID {
				t.Fatal("current Job polling unavailable")
			}
			jobs[i] = env.Data
			switch jobs[i].State {
			case "succeeded", "partial", "failed", "expired", "cancelled":
				terminal++
			}
		}
		if running > peak {
			peak = running
		}
		mainline()
		if terminal == 10 {
			complete = true
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
	if !complete || peak != 10 || mainlineReads < 4 {
		t.Fatalf("ten concurrent investigations not established: peak=%d terminal=%v mainline=%d", peak, complete, mainlineReads)
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal("read-only transaction unavailable")
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT set_config('app.tenant_id',$1,true)`, jobs[0].TenantID.String()); err != nil {
		t.Fatal("tenant read fence unavailable")
	}
	models, tools := int64(0), int64(0)
	for i, j := range jobs {
		if !j.Budget.Valid() || !j.Consumed.Within(j.Budget.Usage) || j.Reserved != (investigation.Usage{}) {
			t.Fatal("terminal concurrent budget exceeded or reservation unsettled")
		}
		models += j.Consumed.ModelRequests
		tools += j.Consumed.ToolCalls
		var audits, events int64
		if tx.QueryRow(ctx, `SELECT count(*) FROM audit.records WHERE tenant_id=$1 AND entity_id=$2 AND event_type LIKE 'investigation.%'`, j.TenantID, j.JobID).Scan(&audits) != nil || tx.QueryRow(ctx, `SELECT count(*) FROM investigation.events WHERE tenant_id=$1 AND job_id=$2`, j.TenantID, j.JobID).Scan(&events) != nil || audits != j.EventSeq || events != j.EventSeq {
			t.Fatal("concurrent ledger/event/Audit atomicity differs")
		}
		status, raw, err := call(http.MethodPost, "/api/v1/incidents/"+incidents[i]+"/investigations", keys[i])
		var replay struct {
			Data investigation.Job `json:"data"`
		}
		if err != nil || status != 202 || json.Unmarshal(raw, &replay) != nil || replay.Data.JobID != j.JobID {
			t.Fatal("concurrent request replay created another Job")
		}
		status, raw, err = call(http.MethodGet, "/api/v1/investigations/"+j.JobID.String()+"/events", "")
		if err != nil || status != 200 {
			t.Fatal("concurrent durable SSE unavailable")
		}
		seq := int64(0)
		for _, line := range strings.Split(string(raw), "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var event struct {
				ID  string `json:"eventId"`
				Job string `json:"jobId"`
			}
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) != nil || event.Job != j.JobID.String() {
				t.Fatal("SSE crossed concurrent Job identity")
			}
			id, err := strconv.ParseInt(event.ID, 10, 64)
			seq++
			if err != nil || id != seq {
				t.Fatal("concurrent SSE gap or reorder")
			}
		}
		if seq != j.EventSeq {
			t.Fatal("concurrent SSE differs from persisted ledger")
		}
	}
	if models < 10 || tools < 10 {
		t.Fatal("real model and MCP participation missing across concurrent investigations")
	}
	receipt["realModelRequests"] = models
	receipt["realMCPCalls"] = tools
	receipt["ledgerAuditAndSSEVerified"] = true
}
