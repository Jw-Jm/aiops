package httpapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"io"
	"net/http"
	"ops-platform/internal/audit"
	"ops-platform/internal/auth"
	"ops-platform/internal/authz"
	"ops-platform/internal/graph"
	"ops-platform/internal/persistence"
	"strconv"
	"strings"
	"time"
)

type auditCursor struct {
	Tenant  uuid.UUID `json:"tenant"`
	Subject string    `json:"subject"`
	Filter  string    `json:"filter"`
	Before  int64     `json:"before"`
	Expires int64     `json:"expires"`
}

func auditFilter(r *http.Request) (map[string]string, error) {
	allowed := map[string]bool{"subject": true, "operation": true, "incidentId": true, "actionPlanId": true, "resource": true, "from": true, "to": true, "cursor": true, "limit": true, "verifyChain": true}
	out := map[string]string{}
	for k, v := range r.URL.Query() {
		if !allowed[k] || len(v) != 1 || len(v[0]) > 4096 {
			return nil, errors.New("invalid filter")
		}
		if k != "cursor" && k != "limit" && k != "verifyChain" {
			out[k] = v[0]
		}
	}
	for _, k := range []string{"from", "to"} {
		if v := out[k]; v != "" {
			if _, e := time.Parse(time.RFC3339Nano, v); e != nil {
				return nil, e
			}
		}
	}
	if out["from"] != "" && out["to"] != "" {
		a, _ := time.Parse(time.RFC3339Nano, out["from"])
		b, _ := time.Parse(time.RFC3339Nano, out["to"])
		if a.After(b) {
			return nil, errors.New("reversed time range")
		}
	}
	return out, nil
}
func auditAdmin(ctx context.Context, tx pgx.Tx, a auth.RequestContext) error {
	if err := authz.RequireAdmin(ctx, tx, a); err != nil {
		if errors.Is(err, authz.ErrUnauthorized) || errors.Is(err, authz.ErrIdentityMismatch) {
			return auth.ErrForbidden
		}
		return err
	}
	return nil
}
func (h *SP04Handlers) serveAudit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vary", "Authorization, X-Tenant-ID")
	a, ok := auth.RequestContextFromContext(r.Context())
	if !ok {
		writeTenantError(w, 401, "UNAUTHENTICATED", "verified identity required", false, "")
		return
	}
	if !auth.HasRole(r.Context(), auth.PlatformAdmin) {
		writeTenantError(w, 403, "FORBIDDEN", "platform_admin required", false, a.RequestID)
		return
	}
	if r.Method != "GET" {
		writeTenantError(w, 405, "METHOD_NOT_ALLOWED", "read-only audit API", false, a.RequestID)
		return
	}
	filters, err := auditFilter(r)
	if err != nil {
		writeTenantError(w, 400, "INVALID_ARGUMENT", "audit filters invalid", false, a.RequestID)
		return
	}
	limit := 50
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
	}
	if err != nil || limit < 1 || limit > 200 {
		writeTenantError(w, 400, "INVALID_ARGUMENT", "limit must be 1..200", false, a.RequestID)
		return
	}
	fb, _ := json.Marshal(filters)
	filterSum := sha256.Sum256(fb)
	filterID := hex.EncodeToString(filterSum[:])
	before := int64(9223372036854775807)
	if token := r.URL.Query().Get("cursor"); token != "" {
		var c auditCursor
		parts := strings.Split(token, ".")
		valid := len(parts) == 2 && len(token) <= 4096 && len(h.SigningKey) == ed25519.PrivateKeySize
		if valid {
			b, e := base64.RawURLEncoding.DecodeString(parts[0])
			sig, se := base64.RawURLEncoding.DecodeString(parts[1])
			valid = e == nil && se == nil && ed25519.Verify(h.SigningKey.Public().(ed25519.PublicKey), b, sig) && json.Unmarshal(b, &c) == nil && c.Tenant == a.TenantID && c.Subject == a.Subject && c.Filter == filterID
		}
		if !valid {
			writeTenantError(w, 400, "INVALID_ARGUMENT", "audit cursor invalid for current filter", false, a.RequestID)
			return
		}
		if c.Expires <= time.Now().Unix() {
			writeTenantError(w, 410, "CURSOR_EXPIRED", "reload the first audit page", false, a.RequestID)
			return
		}
		before = c.Before
	}
	if v := r.URL.Query().Get("verifyChain"); v != "" && v != "true" && v != "false" {
		writeTenantError(w, 400, "INVALID_ARGUMENT", "verifyChain must be boolean", false, a.RequestID)
		return
	}
	var id *uuid.UUID
	if tail := strings.TrimPrefix(r.URL.Path, "/api/v1/audit-records/"); r.URL.Path != "/api/v1/audit-records" {
		v, e := uuid.Parse(tail)
		if e != nil {
			writeTenantError(w, 400, "INVALID_ARGUMENT", "audit ID invalid", false, a.RequestID)
			return
		}
		id = &v
		limit = 1
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out := []map[string]any{}
	last := int64(0)
	err = persistence.WithTenantTx(ctx, h.Pool, a.TenantID, func(tx pgx.Tx) error {
		if e := auditAdmin(ctx, tx, a); e != nil {
			return e
		}
		rows, e := tx.Query(ctx, `SELECT ar.audit_seq,ar.record_id,ar.event_type,ar.entity_kind,ar.entity_id,ar.subject,ar.record,ar.canonical_digest,ar.created_at,
 COALESCE(ar.record->>'incidentId',ar.record->>'incident_id',ex.binding->>'incidentId',asm.binding->>'incidentId',ack.binding->>'incidentId',plan.incident_id::text,job.incident_id::text),
 COALESCE(ar.record->>'actionPlanId',ar.record->>'action_plan_id',ex.binding->>'actionPlanId',asm.binding->>'actionPlanId',ack.binding->>'actionPlanId',plan.action_plan_id::text),
 COALESCE(ar.record->>'resourceCanonicalId',ar.record->>'target',ex.target_canonical_id,asm.binding->>'targetCanonicalId',ack.target_canonical_id)
 FROM audit.records ar
 LEFT JOIN action.executions ex ON ex.tenant_id=ar.tenant_id AND ex.execution_id=ar.entity_id AND ar.entity_kind='command'
 LEFT JOIN action.assessments asm ON asm.tenant_id=ar.tenant_id AND asm.assessment_id=ar.entity_id AND ar.entity_kind='command'
 LEFT JOIN action.risk_acknowledgements ack ON ack.tenant_id=ar.tenant_id AND ack.acknowledgement_id=ar.entity_id AND ar.entity_kind='command'
 LEFT JOIN action.plans plan ON plan.tenant_id=ar.tenant_id AND plan.action_plan_id=ar.entity_id AND ar.entity_kind IN ('command','action-plan')
 LEFT JOIN investigation.jobs job ON job.tenant_id=ar.tenant_id AND job.job_id=ar.entity_id AND ar.entity_kind='investigation'
 WHERE ar.tenant_id=$1 AND ar.audit_seq<$2 AND ($3::uuid IS NULL OR ar.record_id=$3)
 AND ($4='' OR ar.subject=$4) AND ($5='' OR ar.event_type=$5)
 AND ($6='' OR ar.record->>'incidentId'=$6 OR ar.record->>'incident_id'=$6 OR (ar.entity_kind='incident' AND ar.entity_id::text=$6) OR ex.binding->>'incidentId'=$6 OR asm.binding->>'incidentId'=$6 OR ack.binding->>'incidentId'=$6 OR plan.incident_id::text=$6 OR job.incident_id::text=$6)
 AND ($7='' OR ar.record->>'actionPlanId'=$7 OR ar.record->>'action_plan_id'=$7 OR ex.binding->>'actionPlanId'=$7 OR asm.binding->>'actionPlanId'=$7 OR ack.binding->>'actionPlanId'=$7 OR plan.action_plan_id::text=$7)
 AND ($8='' OR ar.record->>'resourceCanonicalId'=$8 OR ar.record->>'target'=$8 OR ex.target_canonical_id=$8 OR asm.binding->>'targetCanonicalId'=$8 OR ack.target_canonical_id=$8)
 AND ($9='' OR ar.created_at>=NULLIF($9,'')::timestamptz) AND ($10='' OR ar.created_at<=NULLIF($10,'')::timestamptz)
 ORDER BY ar.audit_seq DESC LIMIT $11`, a.TenantID, before, id, filters["subject"], filters["operation"], filters["incidentId"], filters["actionPlanId"], filters["resource"], filters["from"], filters["to"], limit+1)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var seq int64
			var en audit.Entry
			en.TenantID = a.TenantID
			var entity *uuid.UUID
			var payload, digest []byte
			var created time.Time
			var incidentID, actionPlanID, resourceID *string
			if e = rows.Scan(&seq, &en.RecordID, &en.EventType, &en.EntityKind, &entity, &en.Subject, &payload, &digest, &created, &incidentID, &actionPlanID, &resourceID); e != nil {
				return e
			}
			if entity != nil {
				en.EntityID = *entity
			}
			en.CreatedAt = created
			d := json.NewDecoder(bytes.NewReader(payload))
			d.UseNumber()
			if e = d.Decode(&en.Payload); e != nil {
				return e
			}
			proof, modern, verifyErr := audit.RecordProof(en, digest)
			row := map[string]any{"incidentId": incidentID, "actionPlanId": actionPlanID, "resource": resourceID, "auditId": en.RecordID, "auditSeq": strconv.FormatInt(seq, 10), "eventType": en.EventType, "entityKind": en.EntityKind, "entityId": entity, "subject": en.Subject, "record": en.Payload, "createdAt": created, "canonicalDigest": "sha256:" + hex.EncodeToString(digest), "digestVerified": verifyErr == nil, "digestFormat": "canonical-v2", "chainVerification": "unverified"}
			if !modern {
				row["digestFormat"] = "legacy"
			}
			if verifyErr == nil {
				row["canonicalJSON"] = string(proof)
				sum := sha256.Sum256(proof)
				row["exportDigest"] = "sha256:" + hex.EncodeToString(sum[:])
			}
			out = append(out, row)
			last = seq
		}
		if e = rows.Err(); e != nil {
			return e
		}
		rows.Close()
		return auditAdmin(ctx, tx, a)
	})
	if err != nil {
		if errors.Is(err, auth.ErrForbidden) {
			writeTenantError(w, 403, "FORBIDDEN", "current platform_admin grant required", false, a.RequestID)
		} else {
			writeTenantError(w, 503, "UNAVAILABLE", "authorized audit read unavailable", true, a.RequestID)
		}
		return
	}
	if id != nil {
		if len(out) == 0 {
			writeTenantError(w, 404, "NOT_FOUND", "audit record unavailable", false, a.RequestID)
			return
		}
		writeJSON(w, 200, map[string]any{"data": out[0], "requestId": a.RequestID})
		return
	}
	var next any
	if len(out) > limit {
		out = out[:limit]
		last, _ = strconv.ParseInt(out[len(out)-1]["auditSeq"].(string), 10, 64)
		if len(h.SigningKey) != ed25519.PrivateKeySize {
			writeTenantError(w, 503, "UNAVAILABLE", "audit pagination key unavailable", true, a.RequestID)
			return
		}
		b, _ := json.Marshal(auditCursor{a.TenantID, a.Subject, filterID, last, time.Now().Add(10 * time.Minute).Unix()})
		next = base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(h.SigningKey, b))
	}
	chain, reason := "unverified", "record digests and signed archive chain are separate; explicitly verify this bounded page range"
	if r.URL.Query().Get("verifyChain") == "true" && len(out) > 0 {
		chain, reason = h.verifyAuditRange(r.Context(), a, out)
	}
	if e := persistence.WithTenantTx(r.Context(), h.Pool, a.TenantID, func(tx pgx.Tx) error { return auditAdmin(r.Context(), tx, a) }); e != nil {
		writeTenantError(w, 403, "FORBIDDEN", "current platform_admin grant required", false, a.RequestID)
		return
	}
	writeJSON(w, 200, map[string]any{"data": out, "meta": map[string]any{"nextCursor": next, "chainVerification": chain, "chainReason": reason}, "requestId": a.RequestID})
}

func (h *SP04Handlers) verifyAuditRange(ctx context.Context, a auth.RequestContext, rows []map[string]any) (string, string) {
	allowed := h.AuditEndpointAllowed
	if allowed == nil {
		allowed = h.EndpointAllowed
	}
	if h.Client == nil || allowed == nil || !allowed(h.AuditEndpoint) || len(h.SigningKey) != ed25519.PrivateKeySize {
		return "unavailable", "archive verifier is not configured"
	}
	body, _ := json.Marshal(map[string]any{"tenantId": a.TenantID, "first": rows[len(rows)-1]["auditSeq"], "last": rows[0]["auditSeq"]})
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	token, err := graph.SignContext(h.SigningKey, internalClaims(nativeProofFromContext(ctx), graph.Claims{Audience: "platform-audit-verify", Subject: a.Subject, Scope: graph.Scope{Tenant: a.TenantID.String(), Cluster: "audit"}, Deadline: deadline, QueryDigest: graph.ScopeDigest(json.RawMessage(body))}))
	if err != nil {
		return "unavailable", "verification context unavailable"
	}
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimSuffix(h.AuditEndpoint, "/")+"/internal/v1/audit:verify", bytes.NewReader(body))
	if err != nil {
		return "unavailable", "verification endpoint invalid"
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Graph-Context", token)
	response, err := h.Client.Do(req)
	if err != nil {
		return "unavailable", "archive verifier connection or deadline unavailable"
	}
	defer response.Body.Close()
	var result map[string]string
	if json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&result) != nil {
		return "unavailable", "archive verifier response invalid"
	}
	if response.StatusCode != 200 {
		if result["code"] == "AUDIT_RANGE_BUDGET" {
			return "partial", "page spans more than 200 tenant records; narrow the time/filter range"
		}
		return "unavailable", "archive verifier unavailable or current permission changed"
	}
	if result["status"] != "verified" && result["status"] != "failed" {
		return "unavailable", "archive verifier status invalid"
	}
	return result["status"], result["reason"]
}
