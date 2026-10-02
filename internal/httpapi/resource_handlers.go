package httpapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"io"
	"net/http"
	"ops-platform/internal/auth"
	"ops-platform/internal/contract"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/persistence"
	"ops-platform/internal/resource"
	"strconv"
	"strings"
	"time"
)

type SP04Handlers struct {
	Pool            persistence.TxBeginner
	Client          *http.Client
	SigningKey      ed25519.PrivateKey
	EndpointAllowed func(string) bool
}

func (h *SP04Handlers) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vary", "Authorization")
	request, ok := auth.RequestContextFromContext(r.Context())
	if !ok {
		writeSP04Error(w, 401, "UNAUTHENTICATED", "verified identity required", false, "")
		return
	}
	fail := func(status int, code string) {
		writeSP04Error(w, status, code, "resource or evidence query unavailable", status == 503, request.RequestID)
	}
	if h.Pool == nil {
		fail(503, "GRAPH_NOT_READY")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/evidence/") && r.Method == "GET" {
		h.getEvidence(w, r, request)
		return
	}
	if r.URL.Path == "/api/v1/admin/legal-holds" {
		h.legalHold(w, r, request)
		return
	}
	if h.Client == nil || len(h.SigningKey) != ed25519.PrivateKeySize || h.EndpointAllowed == nil {
		fail(503, "GRAPH_NOT_READY")
		return
	}
	if r.URL.Path != "/api/v1/diagnostic-graphs:build" && r.URL.Path != "/api/v1/evidence:query" && r.Method != "GET" {
		fail(405, "INVALID_ARGUMENT")
		return
	}
	q := graph.Query{MaxDepth: 2, MaxNodes: 200, MaxEdges: 400, RelationKinds: []string{}}
	cluster := r.URL.Query().Get("clusterUid")
	q.CanonicalID = r.URL.Query().Get("canonicalId")
	switch r.URL.Path {
	case "/api/v1/resources":
		q.QueryKind = "list"
		q.Kind = r.URL.Query().Get("kind")
		q.Namespace = r.URL.Query().Get("namespace")
		q.Name = r.URL.Query().Get("name")
		q.Health = r.URL.Query().Get("health")
		q.Label = r.URL.Query().Get("label")
		q.Sort = r.URL.Query().Get("sort")
		q.Order = r.URL.Query().Get("order")
	case "/api/v1/resources/by-canonical-id":
		q.QueryKind = "entity"
	case "/api/v1/resources/neighbors":
		parsed, err := neighborInput(r)
		if err != nil {
			fail(400, "INVALID_ARGUMENT")
			return
		}
		parsed.CanonicalID = q.CanonicalID
		q = parsed
	case "/api/v1/resources/impact-scope":
		q.QueryKind = "impact"
	case "/api/v1/diagnostic-graphs:build":
		q.QueryKind = "diagnostic"
		parsed, budget, err := diagnosticInput(r)
		if r.Method != "POST" || err != nil {
			fail(400, "INVALID_ARGUMENT")
			return
		}
		q = parsed
		bounded, cancel := context.WithTimeout(r.Context(), budget)
		defer cancel()
		r = r.WithContext(bounded)

	default:
		if r.URL.Path == "/api/v1/evidence:query" {
			h.queryEvidence(w, r, request)
			return
		}
		fail(404, "NOT_FOUND")
		return
	}
	if q.QueryKind != "list" {
		id, err := resource.ParseCanonicalID(q.CanonicalID)
		if err != nil || id.Tenant != request.TenantID.String() {
			fail(403, "FORBIDDEN")
			return
		}
		cluster = id.Scope
	}
	scope, err := (graph.Authorization{Pool: h.Pool}).Effective(r.Context(), request.TenantID.String(), request.Subject, cluster)
	if err != nil {
		fail(403, "FORBIDDEN")
		return
	}
	q.Scope = scope
	if r.Method == "POST" {
		if _, err := h.bindSP04Request(r.Context(), request, "diagnostic-graph", r.Header.Get("Idempotency-Key"), graph.ScopeDigest(q)); err != nil {
			code := "INVALID_ARGUMENT"
			status := 400
			if errors.Is(err, errSP04KeyReuse) {
				code = "IDEMPOTENCY_CONFLICT"
				status = 409
			}
			fail(status, code)
			return
		}
	}
	route, err := (graph.Repository{Pool: h.Pool}).Load(r.Context(), scope.Tenant, cluster)
	if err != nil || route.Endpoint == "" || !time.Now().Before(route.ExpiresAt) || !h.EndpointAllowed(route.Endpoint) {
		fail(503, "GRAPH_NOT_READY")
		return
	}
	q.ExpectedOwnerEpoch = route.Epoch
	if limit := r.URL.Query().Get("limit"); limit != "" && q.QueryKind == "list" {
		q.MaxNodes, err = strconv.Atoi(limit)
		if err != nil || q.MaxNodes < 1 || q.MaxNodes > 200 {
			fail(400, "INVALID_ARGUMENT")
			return
		}
	}
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		var bound graphCursor
		if decodeCursor(h.SigningKey.Public().(ed25519.PublicKey), cursor, &bound) != nil || bound.ScopeDigest != graph.ScopeDigest(scope) || bound.QueryDigest != graph.QueryDigest(q) {
			fail(409, "CONFLICT")
			return
		}
		q.CursorRevision = &bound.Revision
		q.AfterCanonicalID = bound.After
	}
	raw, _ := json.Marshal(q)
	boundedRead, cancelRead := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancelRead()
	reply, err := generationRead(boundedRead, q.CursorRevision != nil, func() ([]byte, error) {
		return h.call(boundedRead, route.Endpoint, "/internal/v1/graph:query", "platform-graph-query", request.Subject, scope, raw)
	})
	if err != nil {
		h.writeCallError(w, err, request.RequestID)
		return
	}
	var result graph.Result
	if contract.Validate("https://ops.local/schemas/resource-graph/v2", reply) != nil || json.Unmarshal(reply, &result) != nil || result.GraphRevision.OwnerEpoch != route.Epoch || result.OwnerInstance != route.Instance {
		fail(503, "GRAPH_NOT_READY")
		return
	}
	visible := map[string]bool{}
	for _, n := range result.Nodes {
		if !scope.Allows(n.CanonicalID, n.Namespace) {
			fail(403, "FORBIDDEN")
			return
		}
		visible[n.CanonicalID] = true
	}
	for _, edge := range result.Edges {
		if !visible[edge.From] || !visible[edge.To] {
			fail(403, "FORBIDDEN")
			return
		}
	}
	finalRoute, err := (graph.Repository{Pool: h.Pool}).Load(r.Context(), scope.Tenant, scope.Cluster)
	if err != nil || finalRoute.Epoch != route.Epoch || finalRoute.Instance != route.Instance || finalRoute.LeaseUID != route.LeaseUID || !time.Now().Before(finalRoute.ExpiresAt) {
		fail(503, "GRAPH_NOT_READY")
		return
	}
	current, err := (graph.Authorization{Pool: h.Pool}).Effective(r.Context(), scope.Tenant, request.Subject, cluster)
	if err != nil || graph.ScopeDigest(current) != graph.ScopeDigest(scope) {
		fail(409, "CONFLICT")
		return
	}
	next := ""
	if result.NextCanonicalID != "" {
		bound := graphCursor{ScopeDigest: graph.ScopeDigest(scope), QueryDigest: graph.QueryDigest(q), Revision: result.GraphRevision, After: result.NextCanonicalID}
		next, _ = encodeCursor(h.SigningKey, bound)
	}
	if q.QueryKind == "list" {
		writeJSON(w, 200, map[string]any{"requestId": request.RequestID, "data": result.Nodes, "meta": map[string]any{"nextCursor": next, "schemaVersion": result.SchemaVersion, "graphRevision": result.GraphRevision, "freshness": result.Freshness, "partial": result.Partial, "degradedSources": result.DegradedSources, "warnings": result.Warnings}})
		return
	}
	writeJSON(w, 200, map[string]any{"requestId": request.RequestID, "data": result, "meta": map[string]any{"nextCursor": next}})
}
func decodeSP04(r *http.Request, out any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, (64<<10)+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("multiple payloads")
	}
	return nil
}

type callError struct {
	Status int
	Code   string
}

func (e callError) Error() string { return e.Code }
func (h *SP04Handlers) call(ctx context.Context, endpoint, path, audience, subject string, scope graph.Scope, payload []byte) ([]byte, error) {
	deadline, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	expires, _ := deadline.Deadline()
	token, err := graph.SignContext(h.SigningKey, graph.Claims{Audience: audience, Subject: subject, Scope: scope, Deadline: expires, QueryDigest: graph.ScopeDigest(json.RawMessage(payload))})
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(deadline, "POST", strings.TrimSuffix(endpoint, "/")+path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Graph-Context", token)
	res, err := h.Client.Do(req)
	if err != nil {
		return nil, callError{503, "GRAPH_NOT_READY"}
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 {
		return nil, callError{503, "GRAPH_NOT_READY"}
	}
	if res.StatusCode != 200 {
		var e struct{ Code string }
		json.Unmarshal(raw, &e)
		switch e.Code {
		case "CAPABILITY_DISABLED", "SOURCE_CAPABILITY_UNAVAILABLE", "SOURCE_DEGRADED", "FORBIDDEN", "STALE_CONTEXT", "SOURCE_SCOPE_UNVERIFIED", "GRAPH_NOT_READY", "INVALID_ARGUMENT", "BUDGET_EXHAUSTED":
			return nil, callError{res.StatusCode, e.Code}
		default:
			return nil, callError{503, "GRAPH_NOT_READY"}
		}
	}
	return raw, nil
}
func (h *SP04Handlers) writeCallError(w http.ResponseWriter, err error, request string) {
	var e callError
	if errors.As(err, &e) {
		writeSP04Error(w, e.Status, publicSP04Code(e.Code), "resource or evidence query unavailable", e.Status == 503, request)
		return
	}
	writeSP04Error(w, 503, "GRAPH_NOT_READY", "resource or evidence query unavailable", true, request)
}

type graphCursor struct {
	ScopeDigest string         `json:"scopeDigest"`
	QueryDigest string         `json:"queryDigest"`
	Revision    graph.Revision `json:"revision"`
	After       string         `json:"after"`
}

func encodeCursor(key ed25519.PrivateKey, c graphCursor) (string, error) {
	raw, _ := json.Marshal(c)
	return graph.SignContext(key, graph.Claims{Audience: "platform-graph-cursor", Subject: "cursor", Deadline: time.Now().Add(25 * time.Second), QueryDigest: graph.ScopeDigest(json.RawMessage(raw)), Scope: graph.Scope{Resources: []string{base64.RawURLEncoding.EncodeToString(raw)}}})
}
func decodeCursor(key ed25519.PublicKey, token string, c *graphCursor) error {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return graph.ErrScope
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return err
	}
	var claims graph.Claims
	if json.Unmarshal(raw, &claims) != nil || len(claims.Scope.Resources) != 1 {
		return graph.ErrScope
	}
	payload, err := base64.RawURLEncoding.DecodeString(claims.Scope.Resources[0])
	if err != nil {
		return err
	}
	if _, err := graph.VerifyContext(key, token, "platform-graph-cursor", payload); err != nil {
		return err
	}
	return json.Unmarshal(payload, c)
}
func (h *SP04Handlers) queryEvidence(w http.ResponseWriter, r *http.Request, request auth.RequestContext) {
	var body struct {
		ResourceCanonicalID string `json:"resourceCanonicalId"`
		Type                string `json:"type"`
		Template            string `json:"queryTemplate"`
		TimeRange           *struct {
			From time.Time `json:"from"`
			To   time.Time `json:"to"`
		} `json:"timeRange"`
		Budget *struct {
			TimeoutMillis int `json:"timeoutMs"`
			MaxBytes      int `json:"maxBytes"`
		} `json:"budget"`
		SourceID  string `json:"sourceRegistrationId,omitempty"`
		Namespace string `json:"namespace,omitempty"`
		Limit     int    `json:"limit,omitempty"`
	}
	if r.Method != "POST" || decodeSP04(r, &body) != nil || body.TimeRange == nil || body.Budget == nil || body.Budget.TimeoutMillis < 1 || body.Budget.MaxBytes < 1 {
		writeSP04Error(w, 400, "INVALID_ARGUMENT", "invalid evidence query", false, request.RequestID)
		return
	}
	var q struct {
		EvidenceID string         `json:"evidenceId"`
		SourceID   string         `json:"sourceRegistrationId"`
		Query      evidence.Query `json:"query"`
	}
	q.SourceID = body.SourceID
	limit := body.Limit
	if limit == 0 {
		limit = 200
	}
	if limit < 1 || limit > 200 {
		writeSP04Error(w, 429, "BUDGET_EXHAUSTED", "row limit invalid", false, request.RequestID)
		return
	}
	q.Query = evidence.Query{ResourceCanonicalID: body.ResourceCanonicalID, Namespace: body.Namespace, Template: body.Template, From: body.TimeRange.From, To: body.TimeRange.To, Limit: limit, MaxBytes: min(body.Budget.MaxBytes, 64<<10), TimeoutMillis: min(body.Budget.TimeoutMillis, 3000)}
	id, err := resource.ParseCanonicalID(q.Query.ResourceCanonicalID)
	if err != nil || id.Tenant != request.TenantID.String() {
		writeSP04Error(w, 403, "FORBIDDEN", "resource scope denied", false, request.RequestID)
		return
	}
	typeName := map[string]string{"metric": "victoriametrics", "metrics": "victoriametrics", "log": "victorialogs", "logs": "victorialogs", "network": "deepflow", "deepflow": "deepflow", "hardware": "redfish"}[body.Type]
	if typeName == "" {
		writeSP04Error(w, 400, "INVALID_ARGUMENT", "source type invalid", false, request.RequestID)
		return
	}
	scope, err := (graph.Authorization{Pool: h.Pool}).Effective(r.Context(), id.Tenant, request.Subject, id.Scope)
	if err != nil {
		writeSP04Error(w, 403, "FORBIDDEN", "resource scope denied", false, request.RequestID)
		return
	}
	err = persistence.WithTenantTx(r.Context(), h.Pool, request.TenantID, func(tx pgx.Tx) error {
		var namespace string
		if err := tx.QueryRow(r.Context(), `SELECT namespace FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2 AND deleted_at IS NULL`, request.TenantID, q.Query.ResourceCanonicalID).Scan(&namespace); err != nil {
			return err
		}
		if !scope.Allows(q.Query.ResourceCanonicalID, namespace) || (body.Namespace != "" && body.Namespace != namespace) {
			return graph.ErrScope
		}
		q.Query.Namespace = namespace
		rows, err := tx.Query(r.Context(), `SELECT s.source_id FROM platform.source_registrations s JOIN platform.cluster_registrations c USING(tenant_id,cluster_id) WHERE s.tenant_id=$1 AND s.source_type=$2 AND s.status='active' AND c.cluster_uid=$3 AND c.status='active' AND ($4='' OR s.source_id::text=$4) ORDER BY s.source_id LIMIT 2`, request.TenantID, typeName, id.Scope, body.SourceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		sources := []string{}
		for rows.Next() {
			var source string
			if err := rows.Scan(&source); err != nil {
				return err
			}
			sources = append(sources, source)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(sources) != 1 {
			return evidence.ErrScopeUnverified
		}
		q.SourceID = sources[0]
		return nil
	})
	if err != nil {
		if errors.Is(err, evidence.ErrScopeUnverified) {
			writeSP04Error(w, 503, "SOURCE_SCOPE_UNVERIFIED", "one explicit qualified source is required", true, request.RequestID)
			return
		}
		writeSP04Error(w, 403, "FORBIDDEN", "source or resource scope unavailable", false, request.RequestID)
		return
	}

	objectID, keyErr := h.bindSP04Request(r.Context(), request, "evidence-query", r.Header.Get("Idempotency-Key"), graph.ScopeDigest(struct {
		Query any
		Scope graph.Scope
	}{q, scope}))
	if keyErr != nil {
		code := "INVALID_ARGUMENT"
		status := 400
		if errors.Is(keyErr, errSP04KeyReuse) {
			code = "IDEMPOTENCY_CONFLICT"
			status = 409
		}
		writeSP04Error(w, status, code, "idempotency key unavailable", false, request.RequestID)
		return
	}
	q.EvidenceID = objectID.String()
	route, err := (graph.Repository{Pool: h.Pool}).Load(r.Context(), id.Tenant, id.Scope)
	if err != nil || !time.Now().Before(route.ExpiresAt) || !h.EndpointAllowed(route.Endpoint) {
		writeSP04Error(w, 503, "GRAPH_NOT_READY", "worker route unavailable", true, request.RequestID)
		return
	}
	raw, _ := json.Marshal(q)
	reply, err := h.call(r.Context(), route.Endpoint, "/internal/v1/evidence:query", "platform-evidence-query", request.Subject, scope, raw)
	if err != nil {
		h.writeCallError(w, err, request.RequestID)
		return
	}
	var result evidence.Result
	if contract.Validate("https://ops.local/schemas/evidence-result/v2", reply) != nil || json.Unmarshal(reply, &result) != nil {
		writeSP04Error(w, 503, "GRAPH_NOT_READY", "worker contract invalid", true, request.RequestID)
		return
	}
	current, err := (graph.Authorization{Pool: h.Pool}).Effective(r.Context(), scope.Tenant, request.Subject, scope.Cluster)
	if err != nil || graph.ScopeDigest(current) != graph.ScopeDigest(scope) {
		writeSP04Error(w, 409, "CONFLICT", "authorization changed", false, request.RequestID)
		return
	}
	for _, e := range result.Evidence {
		if e.TenantID != scope.Tenant || !scope.Allows(e.ResourceCanonicalID, q.Query.Namespace) {
			writeSP04Error(w, 403, "FORBIDDEN", "worker scope invalid", false, request.RequestID)
			return
		}
	}
	writeJSON(w, 200, map[string]any{"requestId": request.RequestID, "data": result})
}
func (h *SP04Handlers) getEvidence(w http.ResponseWriter, r *http.Request, request auth.RequestContext) {
	id, err := uuid.Parse(strings.TrimPrefix(r.URL.Path, "/api/v1/evidence/"))
	if err != nil {
		writeSP04Error(w, 400, "INVALID_ARGUMENT", "invalid evidence ID", false, request.RequestID)
		return
	}
	// Retrieve the metadata under tenant RLS, then derive scope from its trusted ID.
	e, err := loadEvidenceForScope(r.Context(), h.Pool, request.TenantID, id)
	if err != nil {
		writeSP04Error(w, 404, "NOT_FOUND", "evidence unavailable", false, request.RequestID)
		return
	}
	canonical, err := resource.ParseCanonicalID(e.ResourceCanonicalID)
	if err != nil {
		writeSP04Error(w, 404, "NOT_FOUND", "evidence unavailable", false, request.RequestID)
		return
	}
	scope, err := (graph.Authorization{Pool: h.Pool}).Effective(r.Context(), request.TenantID.String(), request.Subject, canonical.Scope)
	if err != nil {
		writeSP04Error(w, 403, "FORBIDDEN", "evidence scope denied", false, request.RequestID)
		return
	}
	e, err = (evidence.Repository{Pool: h.Pool}).Get(r.Context(), request.TenantID, id, scope)
	if err != nil {
		writeSP04Error(w, 403, "FORBIDDEN", "evidence scope denied", false, request.RequestID)
		return
	}
	meta := map[string]any{"freshness": "unavailable", "partial": true, "degradedSources": []string{"archive"}, "warnings": []string{"archive_pending"}}
	if e.ReplayState == "archived_verified" {
		if h.Client == nil || len(h.SigningKey) != ed25519.PrivateKeySize || h.EndpointAllowed == nil {
			writeSP04Error(w, 503, "SOURCE_DEGRADED", "archive worker unavailable", true, request.RequestID)
			return
		}
		route, err := (graph.Repository{Pool: h.Pool}).Load(r.Context(), scope.Tenant, scope.Cluster)
		if err != nil || !time.Now().Before(route.ExpiresAt) || !h.EndpointAllowed(route.Endpoint) {
			writeSP04Error(w, 503, "SOURCE_DEGRADED", "archive worker unavailable", true, request.RequestID)
			return
		}
		raw, _ := json.Marshal(map[string]string{"evidenceId": id.String()})
		reply, err := h.call(r.Context(), route.Endpoint, "/internal/v1/evidence:read", "platform-evidence-read", request.Subject, scope, raw)
		if err != nil {
			h.writeCallError(w, err, request.RequestID)
			return
		}
		var read evidence.Evidence
		if contract.Validate("https://ops.local/schemas/evidence/v2", reply) != nil || json.Unmarshal(reply, &read) != nil || read.EvidenceID != id.String() || read.ResourceCanonicalID != e.ResourceCanonicalID || read.TenantID != request.TenantID.String() || read.ArchiveRef == nil || evidence.Digest(read.FactSlice) != read.ContentDigest {
			writeSP04Error(w, 503, "SOURCE_DEGRADED", "archive contract invalid", true, request.RequestID)
			return
		}
		e = read
		meta = map[string]any{"freshness": "archived", "partial": false, "degradedSources": []string{}, "warnings": []string{}}
	}
	current, err := (graph.Authorization{Pool: h.Pool}).Effective(r.Context(), scope.Tenant, request.Subject, scope.Cluster)
	if err != nil || graph.ScopeDigest(current) != graph.ScopeDigest(scope) {
		writeSP04Error(w, 409, "CONFLICT", "authorization changed", false, request.RequestID)
		return
	}
	writeJSON(w, 200, map[string]any{"requestId": request.RequestID, "data": e, "meta": meta})
}

func publicSP04Code(code string) string {
	switch code {
	case "STALE_CONTEXT":
		return "CONFLICT"
	case "SOURCE_CAPABILITY_UNAVAILABLE":
		return "SOURCE_DEGRADED"
	}
	return code
}
