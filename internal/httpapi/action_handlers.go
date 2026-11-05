package httpapi

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"io"
	"net/http"
	"ops-platform/internal/action"
	"ops-platform/internal/auth"
	"ops-platform/internal/persistence"
	"ops-platform/internal/policy"
	"strconv"
	"strings"
	"time"
)

type ActionHandlers struct {
	Service           action.Service
	ConfigurationOnly bool
	CursorKey         ed25519.PrivateKey
}

var executionListStates = map[string]bool{
	"prepared": true, "dispatching": true, "running": true,
	"succeeded": true, "failed": true, "cancelled": true, "execution_unknown": true,
}

type executionListCursor struct {
	Tenant  uuid.UUID `json:"tenant"`
	Subject string    `json:"subject"`
	Before  uuid.UUID `json:"before"`
	Expires int64     `json:"expires"`
}

func decodeAction(r *http.Request, v any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 128<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return action.ErrInvalid
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return action.ErrInvalid
	}
	return nil
}
func actionError(w http.ResponseWriter, err error, rid string) {
	status, code := 503, "SOURCE_DEGRADED"
	switch {
	case errors.Is(err, action.ErrInvalid):
		status, code = 400, "INVALID_ARGUMENT"
	case errors.Is(err, action.ErrDenied), errors.Is(err, policy.ErrPolicyDenied):
		status, code = 403, "FORBIDDEN"
	case errors.Is(err, auth.ErrStepUpInvalid):
		status, code = 403, "STEP_UP_REQUIRED"
	case errors.Is(err, action.ErrAckRequired):
		status, code = 412, "RISK_ACK_REQUIRED"
	case errors.Is(err, action.ErrAckExpired):
		status, code = 412, "RISK_ACK_EXPIRED"
	case errors.Is(err, action.ErrAckScopeMismatch):
		status, code = 409, "RISK_ACK_SCOPE_MISMATCH"
	case errors.Is(err, action.ErrAckCommandMismatch):
		status, code = 409, "RISK_ACK_COMMAND_MISMATCH"
	case errors.Is(err, action.ErrAcknowledgement):
		status, code = 409, "RISK_ACK_COMMAND_MISMATCH"
	case errors.Is(err, action.ErrConflict):
		status, code = 409, "CONFLICT"
	case errors.Is(err, action.ErrCursorExpired):
		status, code = 410, "CURSOR_EXPIRED"
	case errors.Is(err, pgx.ErrNoRows):
		status, code = 404, "NOT_FOUND"
	}
	writeSP04Error(w, status, code, "command request rejected", false, rid)
}
func (h *ActionHandlers) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vary", "Authorization")
	a, ok := auth.RequestContextFromContext(r.Context())
	if !ok {
		actionError(w, action.ErrDenied, "")
		return
	}
	fail := func(err error) { actionError(w, err, a.RequestID) }
	if h.ConfigurationOnly && r.URL.Path != "/api/v1/admin/execution-profiles" && !strings.HasPrefix(r.URL.Path, "/api/v1/admin/execution-profiles/") {
		writeSP04Error(w, 409, "CAPABILITY_DISABLED", "command execution runtime is disabled; configuration management only", false, a.RequestID)
		return
	}

	key := r.Header.Get("Idempotency-Key")
	if r.Method != "GET" && (len(r.Header.Values("Idempotency-Key")) != 1 || !validIdempotencyKey(key)) {
		fail(action.ErrInvalid)
		return
	}
	if _, native := auth.NativeSessionFromContext(r.Context()); native && r.Method == http.MethodPost {
		operation := ""
		switch {
		case r.URL.Path == "/api/v1/admin/execution-profiles":
			operation = "publish-execution-profile"
		case strings.HasPrefix(r.URL.Path, "/api/v1/admin/execution-profiles/") && strings.HasSuffix(r.URL.Path, ":retire"):
			operation = "retire-execution-profile"
		case r.URL.Path == "/api/v1/admin/host-onboardings":
			operation = "onboard-host"
		}
		if operation != "" {
			var intent auth.NativeStepUpIntent
			err := persistence.WithTenantTx(r.Context(), h.Service.Pool, a.TenantID, func(tx pgx.Tx) error {
				var err error
				intent, err = nativeHTTPWriteIntent(r, tx, operation)
				return err
			})
			if err != nil {
				fail(err)
				return
			}
			r = r.WithContext(auth.WithNativeStepUpIntent(r.Context(), intent))
		}
	}
	switch r.URL.Path {
	case "/api/v1/action-plans":
		if r.Method == "POST" {
			var raw json.RawMessage
			if decodeAction(r, &raw) != nil {
				fail(action.ErrInvalid)
				return
			}
			out, err := h.Service.CreatePlan(r.Context(), a, raw, key)
			if err != nil {
				fail(err)
				return
			}
			writeJSON(w, 201, map[string]any{"data": out, "requestId": a.RequestID})
			return
		}

	case "/api/v1/commands:risk-assess":
		if r.Method != "POST" {
			break
		}
		var in action.CommandRequest
		if decodeAction(r, &in) != nil {
			fail(action.ErrInvalid)
			return
		}
		out, err := h.Service.AssessRisk(r.Context(), a, in, key)
		if err != nil {
			fail(err)
			return
		}
		support := action.PostCheckApplicability(out.Binding.Target)
		out.PostCheckApplicability = &support
		writeJSON(w, 200, map[string]any{"data": out, "requestId": a.RequestID})
		return
	case "/api/v1/commands:risk-acknowledge":
		if r.Method != "POST" {
			break
		}
		var in struct {
			ID     uuid.UUID `json:"assessmentId"`
			Digest string    `json:"executionRequestDigest"`
		}
		if decodeAction(r, &in) != nil {
			fail(action.ErrInvalid)
			return
		}
		out, err := h.Service.AcknowledgeRisk(r.Context(), a, in.ID, in.Digest, key)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, map[string]any{"data": out, "requestId": a.RequestID})
		return
	case "/api/v1/command-executions":
		if r.Method == "GET" {
			limit := 20
			for k, v := range r.URL.Query() {
				if (k != "cursor" && k != "limit" && k != "state") || len(v) != 1 {
					fail(action.ErrInvalid)
					return
				}
			}
			var err error
			if v := r.URL.Query().Get("limit"); v != "" {
				limit, err = strconv.Atoi(v)
			}
			state := r.URL.Query().Get("state")
			if err != nil || limit < 1 || limit > 50 || (state != "" && !executionListStates[state]) {
				fail(action.ErrInvalid)
				return
			}
			var before *uuid.UUID
			if token := r.URL.Query().Get("cursor"); token != "" {
				parts := strings.Split(token, ".")
				var c executionListCursor
				valid := len(parts) == 2 && len(token) <= 4096 && len(h.CursorKey) == ed25519.PrivateKeySize
				if valid {
					b, e := base64.RawURLEncoding.DecodeString(parts[0])
					sig, se := base64.RawURLEncoding.DecodeString(parts[1])
					valid = e == nil && se == nil && ed25519.Verify(h.CursorKey.Public().(ed25519.PublicKey), b, sig) && json.Unmarshal(b, &c) == nil && c.Tenant == a.TenantID && c.Subject == a.Subject
				}
				if !valid {
					fail(action.ErrDenied)
					return
				}
				if c.Expires <= time.Now().Unix() {
					fail(action.ErrCursorExpired)
					return
				}
				before = &c.Before
			}
			page, last, err := h.Service.ListExecutions(r.Context(), a, before, state, limit)
			if err != nil {
				fail(err)
				return
			}
			data := []map[string]any{}
			for _, e := range page {
				data = append(data, e.Public())
			}
			var next any
			if last != nil {
				if len(h.CursorKey) != ed25519.PrivateKeySize {
					fail(action.ErrInvalid)
					return
				}
				b, _ := json.Marshal(executionListCursor{a.TenantID, a.Subject, *last, time.Now().Add(10 * time.Minute).Unix()})
				next = base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(h.CursorKey, b))
			}
			writeJSON(w, 200, map[string]any{"data": data, "meta": map[string]any{"nextCursor": next}, "requestId": a.RequestID})
			return
		}
		if r.Method != "POST" {
			break
		}
		var raw json.RawMessage
		if decodeAction(r, &raw) != nil {
			fail(action.ErrInvalid)
			return
		}
		var in struct {
			action.CommandRequest
			Ack uuid.UUID `json:"riskAcknowledgementId"`
		}
		d := json.NewDecoder(strings.NewReader(string(raw)))
		d.DisallowUnknownFields()
		if d.Decode(&in) != nil {
			fail(action.ErrInvalid)
			return
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			fail(action.ErrInvalid)
			return
		}
		_, present := fields["incidentId"]
		in.OmitIncident = !present
		_, present = fields["executionProfileVersion"]
		in.OmitVersion = !present
		_, present = fields["executionOptions"]
		in.OmitOptions = !present
		out, err := h.Service.PrepareCommandExecution(r.Context(), a, in.CommandRequest, in.Ack, key)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 202, map[string]any{"data": out.Public(), "requestId": a.RequestID})
		return
	case "/api/v1/admin/execution-profiles":
		if r.Method == "GET" {
			limit := registryQueryLimit(r)
			cursor := r.URL.Query().Get("cursor")
			if limit < 1 || len(cursor) > 128 {
				fail(action.ErrInvalid)
				return
			}
			out, err := h.Service.ListProfiles(r.Context(), a)
			if err != nil {
				fail(err)
				return
			}
			page, next, err := paginateExecutionProfiles(out, cursor, limit)
			if err != nil {
				fail(err)
				return
			}
			meta := map[string]any{}
			if next != nil {
				meta["nextCursor"] = *next
			}
			writeJSON(w, 200, map[string]any{"data": page, "meta": meta, "requestId": a.RequestID})
			return
		}
		if r.Method == "POST" {
			var in action.PublishProfileRequest
			if decodeAction(r, &in) != nil {
				fail(action.ErrInvalid)
				return
			}
			if err := h.Service.PublishProfile(r.Context(), a, in, key); err != nil {
				fail(err)
				return
			}
			writeJSON(w, 201, map[string]any{"data": map[string]any{"executionProfileId": in.Profile.ID, "version": in.Profile.Version}, "requestId": a.RequestID})
			return
		}
	}
	if r.URL.Path == "/api/v1/admin/host-onboardings" && r.Method == "POST" {
		var in action.OnboardHostRequest
		if decodeAction(r, &in) != nil {
			fail(action.ErrInvalid)
			return
		}
		if err := h.Service.OnboardHost(r.Context(), a, in, key); err != nil {
			fail(err)
			return
		}
		writeJSON(w, 201, map[string]any{"data": in.Report, "requestId": a.RequestID})
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) == 7 && parts[2] == "admin" && parts[3] == "execution-profiles" && parts[5] == "versions" && strings.HasSuffix(parts[6], ":retire") && r.Method == "POST" {
		id, err := uuid.Parse(parts[4])
		version, verr := strconv.Atoi(strings.TrimSuffix(parts[6], ":retire"))
		if err != nil || verr != nil || version < 1 {
			fail(action.ErrInvalid)
			return
		}
		if err = h.Service.RetireProfile(r.Context(), a, id, version, key); err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, map[string]any{"data": map[string]any{"status": "retired"}, "requestId": a.RequestID})
		return
	}
	if len(parts) == 4 && parts[2] == "action-plans" && r.Method == "POST" {
		state := ""
		idText := parts[3]
		if strings.HasSuffix(idText, ":accept") {
			state = "accepted"
			idText = strings.TrimSuffix(idText, ":accept")
		}
		if strings.HasSuffix(idText, ":dismiss") {
			state = "dismissed"
			idText = strings.TrimSuffix(idText, ":dismiss")
		}
		if state != "" {
			id, err := uuid.Parse(idText)
			if err != nil {
				fail(action.ErrInvalid)
				return
			}
			var in struct{}
			if decodeAction(r, &in) != nil {
				fail(action.ErrInvalid)
				return
			}
			out, err := h.Service.SetPlanState(r.Context(), a, id, state, key)
			if err != nil {
				fail(err)
				return
			}
			writeJSON(w, 200, map[string]any{"data": out, "requestId": a.RequestID})
			return
		}
	}
	if len(parts) == 4 && parts[2] == "action-plans" && r.Method == "GET" {
		id, err := uuid.Parse(parts[3])
		if err != nil {
			fail(action.ErrInvalid)
			return
		}
		out, err := h.Service.GetPlan(r.Context(), a, id)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, map[string]any{"data": out, "requestId": a.RequestID})
		return
	}
	if len(parts) >= 4 && parts[2] == "command-executions" {
		id, err := uuid.Parse(strings.TrimSuffix(parts[3], ":cancel"))
		if err != nil {
			fail(action.ErrInvalid)
			return
		}
		if strings.HasSuffix(parts[3], ":cancel") && len(parts) == 4 && r.Method == "POST" {
			// The formal cancellation operation has no request body.
			if r.ContentLength != 0 {
				var in struct{}
				if decodeAction(r, &in) != nil {
					fail(action.ErrInvalid)
					return
				}
			}
			out, err := h.Service.CancelExecution(r.Context(), a, id, key)
			if err != nil {
				fail(err)
				return
			}
			writeJSON(w, 200, map[string]any{"data": out.Public(), "requestId": a.RequestID})
			return
		}
		if len(parts) == 5 && parts[4] == "post-check" && r.Method == "GET" {
			out, err := h.Service.GetPostCheck(r.Context(), a, id)
			if err != nil {
				fail(err)
				return
			}
			writeJSON(w, 200, map[string]any{"data": out, "requestId": a.RequestID})
			return
		}
		if len(parts) == 5 && parts[4] == "events" && r.Method == "GET" {
			h.stream(w, r, a, id)
			return
		}
		if len(parts) == 4 && r.Method == "GET" {
			out, err := h.Service.Get(r.Context(), a, id)
			if err != nil {
				fail(err)
				return
			}
			writeJSON(w, 200, map[string]any{"data": out.Public(), "requestId": a.RequestID})
			return
		}
	}
	fail(pgx.ErrNoRows)
}
func (h *ActionHandlers) stream(w http.ResponseWriter, r *http.Request, a auth.RequestContext, id uuid.UUID) {
	after := int64(0)
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		prefix := id.String() + ":"
		if !strings.HasPrefix(v, prefix) {
			actionError(w, action.ErrInvalid, a.RequestID)
			return
		}
		n, err := strconv.ParseInt(strings.TrimPrefix(v, prefix), 10, 64)
		if err != nil || n < 0 {
			actionError(w, action.ErrInvalid, a.RequestID)
			return
		}
		after = n
	}
	events, err := h.Service.Events(r.Context(), a, id, after)
	if err != nil {
		actionError(w, err, a.RequestID)
		return
	}
	_, ok := w.(http.Flusher)
	if !ok {
		actionError(w, action.ErrInvalid, a.RequestID)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	controller := http.NewResponseController(w)
	deadline := time.Now().Add(5 * time.Minute)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		for _, e := range events {
			if !time.Now().Before(deadline) || !time.Now().Before(a.TokenExpiresAt) {
				return
			}
			// As for investigation SSE, recheck authority before every disclosure,
			// including events already fetched into this persisted batch.
			if _, err = h.Service.Get(r.Context(), a, id); err != nil {
				return
			}
			b, _ := json.Marshal(e)
			writeDeadline := time.Now().Add(3 * time.Second)
			if a.TokenExpiresAt.Before(writeDeadline) {
				writeDeadline = a.TokenExpiresAt
			}
			if deadline.Before(writeDeadline) {
				writeDeadline = deadline
			}
			_ = controller.SetWriteDeadline(writeDeadline)
			if _, err = io.WriteString(w, "id: "+e.EventID+"\nevent: "+e.EventType+"\ndata: "+string(b)+"\n\n"); err != nil {
				return
			}
			if controller.Flush() != nil {
				return
			}
			after = e.EventSeq
		}
		if !time.Now().Before(deadline) || !time.Now().Before(a.TokenExpiresAt) {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
		events, err = h.Service.Events(r.Context(), a, id, after)
		if err != nil {
			return
		}
	}
}
