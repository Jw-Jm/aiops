//go:build pre_sp07_live

package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/evidence"
	"ops-platform/internal/investigation"
)

// Inspect an already installed, API-created native Job. This gate constructs no
// application, fixture database, business seed, model provider or MCP server.
func TestFormalCurrentInstalledInvestigationLedgerEvidenceAndSSE(t *testing.T) {
	origin := os.Getenv("PRE_SP07_CURRENT_API_LOOPBACK")
	tokenPath := os.Getenv("PRE_SP07_CURRENT_TOKEN_FILE")
	jobID := os.Getenv("PRE_SP07_CURRENT_JOB_ID")
	dsn := os.Getenv("PRE_SP07_CURRENT_READONLY_DATABASE_URL")
	receiptPath := os.Getenv("PRE_SP07_CURRENT_CHAIN_RECEIPT")
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || tokenPath == "" || dsn == "" || receiptPath == "" {
		t.Fatal("actual installed API loopback, private bearer, read-only transaction DSN and receipt output required")
	}
	if _, err := uuid.Parse(jobID); err != nil {
		t.Fatal("actual API-created Job identity required")
	}
	info, err := os.Stat(tokenPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		t.Fatal("private bearer file required")
	}
	token, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal("private bearer unavailable")
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	call := func(path, cursor, tenant string) (int, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, origin+path, nil)
		if err != nil {
			t.Fatal("fixed API request invalid")
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
		if cursor != "" {
			req.Header.Set("Last-Event-ID", cursor)
		}
		if tenant != "" {
			req.Header.Set("X-Tenant-ID", tenant)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal("actual API request failed")
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
		if err != nil || len(raw) > 2<<20 {
			t.Fatal("actual response exceeds bounded read")
		}
		return response.StatusCode, raw
	}
	path := "/api/v1/investigations/" + jobID
	status, raw := call(path, "", "")
	var envelope struct {
		Data investigation.Job `json:"data"`
	}
	if status != 200 || json.Unmarshal(raw, &envelope) != nil {
		t.Fatal("actual Job read failed")
	}
	job := envelope.Data
	if job.JobID.String() != jobID || job.SchemaVersion != "investigation-job/v2" || job.State != "succeeded" && job.State != "partial" || job.Consumed.ToolCalls < 1 || job.Consumed.ModelRequests < 1 || job.Consumed.InputTokens < 1 || job.Consumed.OutputTokens < 1 || job.Reserved != (investigation.Usage{}) {
		t.Fatal("actual model/MCP/budget settlement incomplete")
	}
	var result struct {
		Status       string            `json:"status"`
		EvidenceRefs []string          `json:"evidenceRefs"`
		ActionPlans  []json.RawMessage `json:"actionPlans"`
	}
	if json.Unmarshal(job.Result, &result) != nil || len(result.EvidenceRefs) == 0 {
		t.Fatal("validated actual result lacks retained Evidence")
	}
	status, raw = call(path+"/steps", "", "")
	var steps struct {
		Data []investigation.Step `json:"data"`
	}
	if status != 200 || json.Unmarshal(raw, &steps) != nil {
		t.Fatal("actual Step ledger read failed")
	}
	tools, models := 0, 0
	for _, s := range steps.Data {
		if s.Name == "execute_command" || s.Name == "ssh" || s.Name == "sql" || s.Name == "fetch_url" || s.Name == "kubectl_exec" {
			t.Fatal("prohibited execution/fetch Step present")
		}
		if s.State == "succeeded" {
			if s.Name == "model" {
				models++
			} else {
				tools++
			}
		}
	}
	if tools < 1 || models < 1 {
		t.Fatal("actual standard MCP and model Step completion missing")
	}
	retained := []map[string]any{}
	for _, id := range result.EvidenceRefs {
		status, raw = call("/api/v1/evidence/"+id, "", "")
		var reply struct {
			Data evidence.Evidence `json:"data"`
			Meta struct {
				Freshness string `json:"freshness"`
				Partial   bool   `json:"partial"`
			} `json:"meta"`
		}
		if status != 200 || json.Unmarshal(raw, &reply) != nil || reply.Data.EvidenceID != id || reply.Data.TenantID != job.TenantID.String() || reply.Data.ArchiveRef == nil || reply.Data.ReplayState != "archived_verified" || reply.Meta.Freshness != "archived" || reply.Meta.Partial || evidence.Digest(reply.Data.FactSlice) != reply.Data.ContentDigest {
			t.Fatal("actual authorized archive replay/digest/version incomplete")
		}
		retained = append(retained, map[string]any{"evidenceId": id, "contentDigest": reply.Data.ContentDigest, "resourceCanonicalId": reply.Data.ResourceCanonicalID, "sourceId": reply.Data.SourceRegistrationID, "sourceRevision": reply.Data.SourceRevision, "archiveRef": reply.Data.ArchiveRef})
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal("actual audit database connection unavailable")
	}
	defer conn.Close(context.Background())
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal("read-only verification transaction unavailable")
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT set_config('app.tenant_id',$1,true)`, job.TenantID.String()); err != nil {
		t.Fatal("tenant read scope unavailable")
	}
	var audits, events int64
	if tx.QueryRow(ctx, `SELECT count(*) FROM audit.records WHERE tenant_id=$1 AND entity_id=$2 AND event_type LIKE 'investigation.%'`, job.TenantID, job.JobID).Scan(&audits) != nil || tx.QueryRow(ctx, `SELECT count(*) FROM investigation.events WHERE tenant_id=$1 AND job_id=$2`, job.TenantID, job.JobID).Scan(&events) != nil || audits != job.EventSeq || events != job.EventSeq {
		t.Fatal("persisted Ledger/Audit event atomicity differs")
	}
	status, raw = call(path+"/events", "", "")
	if status != 200 {
		t.Fatal("actual durable SSE unavailable")
	}
	type event struct {
		ID    string `json:"eventId"`
		JobID string `json:"jobId"`
	}
	var sequences []int64
	var cursors []string
	cursor := ""
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "id: ") {
			cursor = strings.TrimPrefix(line, "id: ")
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var e event
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e) != nil || e.JobID != jobID {
			t.Fatal("SSE Job identity differs")
		}
		n, err := strconv.ParseInt(e.ID, 10, 64)
		if err != nil || n != int64(len(sequences)+1) || cursor == "" {
			t.Fatal("durable SSE sequence gap")
		}
		sequences = append(sequences, n)
		cursors = append(cursors, cursor)
	}
	if int64(len(sequences)) != job.EventSeq || len(sequences) < 2 {
		t.Fatal("durable SSE does not match persisted event ledger")
	}
	middle := len(sequences)/2 - 1
	status, raw = call(path+"/events", cursors[middle], "")
	if status != 200 {
		t.Fatal("durable SSE resume rejected")
	}
	next := sequences[middle] + 1
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var e event
		json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e)
		n, err := strconv.ParseInt(e.ID, 10, 64)
		if err != nil || n != next || e.JobID != jobID {
			t.Fatal("SSE resume repeated or reordered an event")
		}
		next++
	}
	if next != job.EventSeq+1 {
		t.Fatal("SSE resume omitted persisted terminal events")
	}
	status, _ = call(path, "", uuid.NewString())
	if status != 403 {
		t.Fatal("foreign tenant header did not fail closed")
	}
	receipt := map[string]any{"observedAt": time.Now().UTC(), "jobId": jobID, "state": job.State, "resultStatus": result.Status, "successfulToolSteps": tools, "successfulModelSteps": models, "budgetConsumed": job.Consumed, "budgetReserved": job.Reserved, "auditRecords": audits, "persistedEvents": events, "sseEvents": len(sequences), "sseResumeAfter": sequences[middle], "crossTenantStatus": status, "retainedEvidence": retained, "accuracyPass": false, "coldInstallPass": false, "fullR0ThroughR6Complete": false}
	encoded, _ := json.MarshalIndent(receipt, "", "  ")
	if os.WriteFile(receiptPath, append(encoded, '\n'), 0644) != nil {
		t.Fatal("public metadata receipt write failed")
	}
	t.Logf("actual installed Holmes/model/MCP result, Go settlement, archive replay, Ledger/Audit and persistent SSE/resume verified: job=%s tools=%d models=%d events=%d", jobID, tools, models, events)
}
