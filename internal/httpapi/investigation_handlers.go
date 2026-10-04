package httpapi

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"io"
	"net/http"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
	"ops-platform/internal/investigation/tools"
	"ops-platform/internal/persistence"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

type InvestigationHandlers struct {
	Repository    investigation.Repository
	Signer        investigation.ContextSigner
	Trust         auth.WorkloadTrust
	CursorKey     ed25519.PrivateKey
	Authorize     func(context.Context, investigation.Job, string, json.RawMessage) error
	PolicyVersion func(context.Context, auth.RequestContext, string) (string, error)
	PolicyBudget  func(context.Context, auth.RequestContext, string, string) (investigation.Budget, error)
	Budget        investigation.Budget
}

func decodeInvestigation(r *http.Request, v any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return investigation.ErrInvalid
	}
	return nil
}
func investigationError(w http.ResponseWriter, err error, rid string) {
	status := 503
	code := "INVESTIGATION_UNAVAILABLE"
	switch {
	case errors.Is(err, investigation.ErrInvalid):
		status, code = 400, "INVALID_ARGUMENT"
	case errors.Is(err, investigation.ErrDenied):
		status, code = 403, "FORBIDDEN"
	case errors.Is(err, investigation.ErrBudget):
		status, code = 429, "BUDGET_EXHAUSTED"
	case errors.Is(err, investigation.ErrConflict):
		status, code = 409, "IDEMPOTENCY_CONFLICT"
	case errors.Is(err, investigation.ErrReplay):
		status, code = 409, "REPLAY_REJECTED"
	case errors.Is(err, investigation.ErrLease):
		status, code = 409, "STALE_CONTEXT"
	case errors.Is(err, pgx.ErrNoRows):
		status, code = 404, "NOT_FOUND"
	}
	writeSP04Error(w, status, code, "investigation request rejected", false, rid)
}
func (h *InvestigationHandlers) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vary", "Authorization")
	actor, ok := auth.RequestContextFromContext(r.Context())
	if !ok || !auth.HasRole(r.Context(), auth.Operator) {
		investigationError(w, investigation.ErrDenied, "")
		return
	}
	path := strings.Trim(r.URL.Path, "/")
	parts := strings.Split(path, "/")
	if len(parts) == 5 && parts[2] == "incidents" && parts[4] == "investigations" && r.Method == "POST" {
		id, err := uuid.Parse(parts[3])
		if err != nil {
			investigationError(w, investigation.ErrInvalid, actor.RequestID)
			return
		}
		var body struct {
			TriggerRevision int64  `json:"triggerRevision"`
			TriggerKind     string `json:"triggerKind"`
		}
		if decodeInvestigation(r, &body) != nil || len(r.Header.Values("Idempotency-Key")) != 1 || !validIdempotencyKey(r.Header.Get("Idempotency-Key")) {
			investigationError(w, investigation.ErrInvalid, actor.RequestID)
			return
		}
		// Human manual triggers only. Automatic trigger kinds are assigned by trusted
		// platform policy events, never promoted from a browser body.
		if body.TriggerKind != "" && body.TriggerKind != "manual" {
			investigationError(w, investigation.ErrDenied, actor.RequestID)
			return
		}
		var cluster string
		var revision int64
		err = persistence.WithTenantTx(r.Context(), h.Repository.Pool, actor.TenantID, func(tx pgx.Tx) error {
			return tx.QueryRow(r.Context(), `SELECT i.revision,f.cluster_uid FROM incident.records i JOIN incident.finding_links l USING(tenant_id,incident_id) JOIN finding.records f USING(tenant_id,finding_id) WHERE i.tenant_id=$1 AND i.incident_id=$2 LIMIT 1`, actor.TenantID, id).Scan(&revision, &cluster)
		})
		if err != nil {
			investigationError(w, err, actor.RequestID)
			return
		}
		if body.TriggerRevision != 0 && body.TriggerRevision != revision {
			investigationError(w, investigation.ErrInvalid, actor.RequestID)
			return
		}
		scope, err := (graph.Authorization{Pool: h.Repository.Pool}).Effective(r.Context(), actor.TenantID.String(), actor.Subject, cluster)
		if err != nil {
			investigationError(w, investigation.ErrDenied, actor.RequestID)
			return
		}
		if h.PolicyVersion == nil {
			investigationError(w, investigation.ErrDenied, actor.RequestID)
			return
		}
		version, err := h.PolicyVersion(r.Context(), actor, cluster)
		if err != nil {
			investigationError(w, investigation.ErrDenied, actor.RequestID)
			return
		}
		defs, err := tools.Catalog()
		if err != nil {
			investigationError(w, err, actor.RequestID)
			return
		}
		budget := h.Budget
		if h.PolicyBudget != nil {
			budget, err = h.PolicyBudget(r.Context(), actor, cluster, version)
			if err != nil {
				investigationError(w, investigation.ErrDenied, actor.RequestID)
				return
			}
		}
		job, err := h.Repository.CreateJob(r.Context(), investigation.CreateInvestigationRequest{IdempotencyKey: r.Header.Get("Idempotency-Key"), RequestDigest: graph.ScopeDigest(struct {
			IncidentID uuid.UUID
			Body       any
		}{id, body}), TenantID: actor.TenantID, Subject: actor.Subject, IncidentID: id, TriggerRevision: revision, TriggerKind: "manual", PolicyVersion: version, Scope: scope, ToolCatalogDigest: tools.Digest(defs), Budget: budget})
		if err != nil {
			investigationError(w, err, actor.RequestID)
			return
		}
		writeJSON(w, 202, map[string]any{"data": job, "requestId": actor.RequestID})
		return
	}
	if len(parts) < 4 || parts[2] != "investigations" {
		investigationError(w, pgx.ErrNoRows, actor.RequestID)
		return
	}
	id, err := uuid.Parse(strings.TrimSuffix(parts[3], ":cancel"))
	if err != nil {
		investigationError(w, investigation.ErrInvalid, actor.RequestID)
		return
	}
	if strings.HasSuffix(parts[3], ":cancel") && len(parts) == 4 && r.Method == "POST" {
		var body map[string]any
		if decodeInvestigation(r, &body) != nil || len(body) > 0 || len(r.Header.Values("Idempotency-Key")) != 1 || !validIdempotencyKey(r.Header.Get("Idempotency-Key")) {
			investigationError(w, investigation.ErrInvalid, actor.RequestID)
			return
		}
		if err = h.Repository.Stop(r.Context(), actor.TenantID, id, actor.Subject, false, r.Header.Get("Idempotency-Key")); err != nil {
			investigationError(w, err, actor.RequestID)
			return
		}
		job, _, err := h.Repository.Events(r.Context(), actor.TenantID, id, actor.Subject, 0)
		if err != nil {
			investigationError(w, err, actor.RequestID)
			return
		}
		writeJSON(w, 200, map[string]any{"data": map[string]any{"state": job.State}, "requestId": actor.RequestID})
		return
	}
	if r.Method != "GET" {
		investigationError(w, investigation.ErrDenied, actor.RequestID)
		return
	}
	if len(parts) == 5 && parts[4] == "events" {
		h.stream(w, r, actor, id)
		return
	}
	job, _, err := h.Repository.Events(r.Context(), actor.TenantID, id, actor.Subject, 0)
	if err != nil {
		investigationError(w, err, actor.RequestID)
		return
	}
	if len(parts) == 4 {
		writeJSON(w, 200, map[string]any{"data": job, "requestId": actor.RequestID})
		return
	}
	if len(parts) == 5 && parts[4] == "steps" {
		steps, err := h.Repository.ReadSteps(r.Context(), actor.TenantID, id, actor.Subject)
		if err != nil {
			investigationError(w, err, actor.RequestID)
			return
		}
		writeJSON(w, 200, map[string]any{"data": steps, "requestId": actor.RequestID})
		return
	}
	investigationError(w, pgx.ErrNoRows, actor.RequestID)
}

type investigationCursor struct {
	Tenant  uuid.UUID `json:"tenant"`
	Job     uuid.UUID `json:"job"`
	Seq     int64     `json:"seq"`
	Scope   string    `json:"scope"`
	Expires int64     `json:"exp"`
	Kind    string    `json:"kind"`
}

func (h *InvestigationHandlers) cursor(c investigationCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(h.CursorKey, b))
}
func (h *InvestigationHandlers) stream(w http.ResponseWriter, r *http.Request, a auth.RequestContext, id uuid.UUID) {
	if len(h.CursorKey) != ed25519.PrivateKeySize {
		investigationError(w, investigation.ErrInvalid, a.RequestID)
		return
	}
	after := int64(0)
	var cursorScope string
	if token := r.Header.Get("Last-Event-ID"); token != "" {
		p := strings.Split(token, ".")
		var c investigationCursor
		if len(p) != 2 || len(token) > 4096 {
			investigationError(w, investigation.ErrInvalid, a.RequestID)
			return
		}
		b, e1 := base64.RawURLEncoding.DecodeString(p[0])
		sig, e2 := base64.RawURLEncoding.DecodeString(p[1])
		if e1 != nil || e2 != nil || !ed25519.Verify(h.CursorKey.Public().(ed25519.PublicKey), b, sig) || json.Unmarshal(b, &c) != nil || c.Tenant != a.TenantID || c.Job != id || c.Kind != "investigation" {
			investigationError(w, investigation.ErrDenied, a.RequestID)
			return
		}
		if c.Expires <= time.Now().Unix() {
			writeSP04Error(w, 410, "CURSOR_EXPIRED", "read the job and steps before resubscribing", false, a.RequestID)
			return
		}
		after, cursorScope = c.Seq, c.Scope
	}
	job, events, err := h.Repository.Events(r.Context(), a.TenantID, id, a.Subject, after)
	if err != nil {
		investigationError(w, err, a.RequestID)
		return
	}
	if cursorScope != "" && cursorScope != job.EffectiveScopeDigest {
		investigationError(w, investigation.ErrDenied, a.RequestID)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	controller := http.NewResponseController(w)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.Now().Add(5 * time.Minute)
	for {
		for _, e := range events {
			if !time.Now().Before(deadline) || (!a.TokenExpiresAt.IsZero() && !time.Now().Before(a.TokenExpiresAt)) {
				return
			}
			// Revalidate before each disclosure, including events already fetched in this batch.
			if _, err = h.Repository.Get(r.Context(), a.TenantID, id); err != nil {
				return
			}
			cursor := h.cursor(investigationCursor{a.TenantID, id, e.EventID, job.EffectiveScopeDigest, time.Now().Add(24 * time.Hour).Unix(), "investigation"})
			eventType := "job.state"
			if e.Type == "step_started" {
				eventType = "step.started"
			} else if e.Type == "step_succeeded" || e.Type == "step_failed" {
				eventType = "step.completed"
			} else if e.Type == "result" {
				eventType = "job.completed"
			}
			envelope := map[string]any{"eventId": strconv.FormatInt(e.EventID, 10), "eventType": eventType, "jobId": id, "occurredAt": e.OccurredAt, "payload": e.Payload}
			b, _ := json.Marshal(envelope)
			writeDeadline := time.Now().Add(3 * time.Second)
			if !a.TokenExpiresAt.IsZero() && a.TokenExpiresAt.Before(writeDeadline) {
				writeDeadline = a.TokenExpiresAt
			}
			if deadline.Before(writeDeadline) {
				writeDeadline = deadline
			}
			_ = controller.SetWriteDeadline(writeDeadline)
			if _, err = fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", cursor, eventType, b); err != nil {
				return
			}
			if controller.Flush() != nil {
				return
			}
			after = e.EventID
		}
		if time.Now().After(deadline) || (!a.TokenExpiresAt.IsZero() && !time.Now().Before(a.TokenExpiresAt)) {
			return
		}
		// Terminal jobs can contain more than one persisted event batch. Drain
		// every authorized batch before closing the stream.
		if job.State != "queued" && job.State != "running" && after >= job.EventSeq {
			return
		}
		if after < job.EventSeq {
			job, events, err = h.Repository.Events(r.Context(), a.TenantID, id, a.Subject, after)
			if err != nil {
				return
			}
			continue
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
		job, events, err = h.Repository.Events(r.Context(), a.TenantID, id, a.Subject, after)
		if err != nil {
			return
		}
	}
}

// InternalHandler accepts only the configured investigator SPIFFE identity and
// audience-bound Context. Neither OIDC users nor Agent model output can use it.
func (h *InvestigationHandlers) InternalHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fail := func(err error) { investigationError(w, err, "") }
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			fail(investigation.ErrDenied)
			return
		}
		workload, err := auth.VerifyWorkload(auth.WithWorkloadTrust(r.Context(), h.Trust), r.TLS.PeerCertificates[0])
		if err != nil || workload.ServiceAccount != "ops-investigator" {
			fail(investigation.ErrDenied)
			return
		}
		fields := strings.Fields(r.Header.Get("Authorization"))
		if len(fields) != 2 || fields[0] != "Bearer" {
			fail(investigation.ErrDenied)
			return
		}
		c, err := h.Signer.VerifyContext(r.Context(), fields[1], "platform-job-api", workload.URI)
		if err != nil {
			fail(err)
			return
		}
		defs, err := tools.Catalog()
		if err != nil {
			fail(err)
			return
		}
		j, l, err := h.Repository.Authenticate(r.Context(), c, tools.Digest(defs))
		if err != nil {
			fail(err)
			return
		}
		r = r.WithContext(investigation.WithDataClasses(r.Context(), c.AllowedDataClasses))
		prefix := "/internal/v1/investigations/" + j.JobID.String()
		if !strings.HasPrefix(r.URL.Path, prefix+"/") {
			fail(investigation.ErrDenied)
			return
		}
		suffix := strings.TrimPrefix(r.URL.Path, prefix)
		reply := func(v any) { writeJSON(w, 200, map[string]any{"data": v}) }
		if suffix == "/result:complete" && r.Method == "POST" {
			var proposal json.RawMessage
			if decodeInvestigation(r, &proposal) != nil {
				fail(investigation.ErrInvalid)
				return
			}
			clean, err := tools.Sanitize(tools.Result{Data: proposal}, 65536)
			if err != nil {
				fail(err)
				return
			}
			raw, _ := json.Marshal(clean.Data)
			if path := os.Getenv("SP06_TEST_PROPOSAL_FILE"); path != "" {
				if os.WriteFile(path, raw, 0600) != nil {
					fail(investigation.ErrInvalid)
					return
				}
			}
			if err = h.Repository.Complete(r.Context(), l, raw); err != nil {
				fail(err)
				return
			}
			final, err := h.Repository.Get(r.Context(), j.TenantID, j.JobID)
			if err != nil {
				fail(err)
				return
			}
			reply(final.Result)
			return
		}
		if suffix == "/contexts:renew" && r.Method == "POST" {
			var body map[string]any
			if decodeInvestigation(r, &body) != nil || len(body) > 0 {
				fail(investigation.ErrInvalid)
				return
			}
			// Renewal can preserve or narrow the admitted Context, never restore
			// permissions omitted by the original issuer.
			names := []string{}
			for _, name := range tools.Names(defs) {
				if slices.Contains(c.AllowedTools, name) {
					names = append(names, name)
				}
			}
			narrowed := j
			narrowed.Budget.AllowedDataClasses = slices.Clone(c.AllowedDataClasses)
			jobToken, err := h.Signer.IssueRegistered(r.Context(), h.Repository, narrowed, l, "platform-job-api", workload.URI, names)
			if err != nil {
				fail(err)
				return
			}
			mcpToken, err := h.Signer.IssueRegistered(r.Context(), h.Repository, narrowed, l, "platform-mcp-gateway", workload.URI, names)
			if err != nil {
				fail(err)
				return
			}
			reply(map[string]string{"jobContext": jobToken, "mcpContext": mcpToken})
			return
		}
		if suffix == "/steps" && r.Method == "GET" {
			v, err := h.Repository.Steps(r.Context(), l)
			if err != nil {
				fail(err)
				return
			}
			reply(v)
			return
		}
		if strings.HasPrefix(suffix, "/steps/") && strings.HasSuffix(suffix, "/result") && r.Method == "GET" {
			id, err := uuid.Parse(strings.TrimSuffix(strings.TrimPrefix(suffix, "/steps/"), "/result"))
			if err != nil {
				fail(investigation.ErrInvalid)
				return
			}
			v, err := h.Repository.RecoverStep(r.Context(), l, id, r.URL.Query().Get("argsDigest"))
			if err != nil {
				fail(err)
				return
			}
			reply(json.RawMessage(v))
			return
		}
		if suffix == "/calls:allocate" && r.Method == "POST" {
			var body struct {
				Name      string              `json:"name"`
				Arguments json.RawMessage     `json:"arguments"`
				Model     bool                `json:"model"`
				Reserve   investigation.Usage `json:"reserve"`
			}
			if decodeInvestigation(r, &body) != nil {
				fail(investigation.ErrInvalid)
				return
			}
			var allocation investigation.Allocation
			if body.Model {
				if body.Name != "model" {
					fail(investigation.ErrInvalid)
					return
				}
				// A model request is an investigation read under the current signed
				// policy. Reuse its Incident-context permission before admission.
				args, _ := json.Marshal(map[string]string{"incidentId": j.IncidentID.String()})
				if c.Bind(j, l, tools.Digest(defs), "get_incident_context") != nil || h.Authorize == nil || h.Authorize(r.Context(), j, "get_incident_context", args) != nil {
					fail(investigation.ErrDenied)
					return
				}
				allocation, err = h.Repository.AllocateModel(r.Context(), l, body.Reserve, body.Arguments)
			} else {
				valid := false
				for _, d := range defs {
					if d.Name == body.Name && d.Disabled == "" && d.Validate(body.Arguments) == nil {
						valid = true
					}
				}
				if !valid {
					fail(investigation.ErrDenied)
					return
				}
				if c.Bind(j, l, tools.Digest(defs), body.Name) != nil || h.Authorize == nil || h.Authorize(r.Context(), j, body.Name, body.Arguments) != nil {
					fail(investigation.ErrDenied)
					return
				}
				allocation, err = h.Repository.AllocateTool(r.Context(), l, body.Name, body.Arguments)
			}
			if err != nil {
				fail(err)
				return
			}
			reply(allocation)
			return
		}
		if strings.HasPrefix(suffix, "/steps/") && strings.HasSuffix(suffix, ":complete") && r.Method == "POST" {
			id, err := uuid.Parse(strings.TrimSuffix(strings.TrimPrefix(suffix, "/steps/"), ":complete"))
			if err != nil {
				fail(investigation.ErrInvalid)
				return
			}
			var body struct {
				Result    json.RawMessage      `json:"result"`
				Consumed  *investigation.Usage `json:"consumed"`
				ErrorCode string               `json:"errorCode"`
			}
			if decodeInvestigation(r, &body) != nil {
				fail(investigation.ErrInvalid)
				return
			}
			if body.ErrorCode == "" {
				cleaned, err := tools.Sanitize(tools.Result{Data: body.Result}, 65536)
				if err != nil {
					fail(err)
					return
				}
				body.Result, _ = json.Marshal(cleaned.Data)
			}
			if err = h.Repository.SettleModel(r.Context(), l, id, body.Result, body.Consumed, body.ErrorCode); err != nil {
				fail(err)
				return
			}
			reply(map[string]any{"settled": true})
			return
		}
		fail(investigation.ErrDenied)
	})
}
