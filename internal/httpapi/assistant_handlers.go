package httpapi

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/auth"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
)

type AssistantHandlers struct {
	Repository     assistant.Repository
	SP04           *SP04Handlers
	CursorKey      ed25519.PrivateKey
	ModelTaskKinds []string
}
type assistantCursor struct {
	Tenant         uuid.UUID `json:"tenant"`
	Subject        string    `json:"subject"`
	Kind           string    `json:"kind"`
	ConversationID uuid.UUID `json:"conversationId"`
	ScopeDigest    string    `json:"scopeDigest"`
	Sequence       int64     `json:"sequence"`
	Before         time.Time `json:"before"`
	BeforeID       uuid.UUID `json:"beforeId"`
	ExpiresAt      int64     `json:"expiresAt"`
}

func assistantError(w http.ResponseWriter, err error, rid string) {
	switch {
	case err != nil && err.Error() == "CURSOR_EXPIRED":
		writeSP04Error(w, http.StatusGone, "CURSOR_EXPIRED", "cursor expired; refresh the conversation", false, rid)
	case errors.Is(err, assistant.ErrBusy), errors.Is(err, assistant.ErrStale), errors.Is(err, assistant.ErrArchived):
		writeSP04Error(w, 409, err.Error(), "conversation cannot accept this operation", false, rid)
	case errors.Is(err, analytics.ErrUnsupported):
		writeSP04Error(w, 422, "UNSUPPORTED_QUERY", "dataset or operation is unsupported", false, rid)
	case errors.Is(err, analytics.ErrInvalid):
		investigationError(w, investigation.ErrInvalid, rid)
	case errors.Is(err, analytics.ErrDenied), errors.Is(err, analytics.ErrStale):
		investigationError(w, investigation.ErrDenied, rid)
	default:
		investigationError(w, err, rid)
	}
}
func (h *AssistantHandlers) decodeCursor(token string, actor auth.RequestContext, kind string) (assistantCursor, error) {
	var c assistantCursor
	if token == "" {
		return c, nil
	}
	if len(h.CursorKey) != ed25519.PrivateKeySize || decodeAssistantCursor(h.CursorKey.Public().(ed25519.PublicKey), token, &c) != nil || c.Tenant != actor.TenantID || c.Subject != actor.Subject || c.Kind != kind {
		return c, investigation.ErrDenied
	}
	if c.ExpiresAt <= time.Now().Unix() {
		return c, errors.New("CURSOR_EXPIRED")
	}
	return c, nil
}
func (h *AssistantHandlers) cursor(actor auth.RequestContext, c assistantCursor) string {
	c.Tenant = actor.TenantID
	c.Subject = actor.Subject
	c.ExpiresAt = time.Now().Add(24 * time.Hour).Unix()
	b, _ := json.Marshal(c)
	out := base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(h.CursorKey, b))
	return out
}
func (h *AssistantHandlers) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vary", "Authorization")
	actor, ok := auth.RequestContextFromContext(r.Context())
	if !ok {
		investigationError(w, investigation.ErrDenied, "")
		return
	}
	fail := func(err error) { assistantError(w, err, actor.RequestID) }
	if h.Repository.Pool == nil || len(h.CursorKey) != ed25519.PrivateKeySize {
		fail(investigation.ErrNoJob)
		return
	}
	if r.URL.Path == "/api/v1/assistant/capabilities" && r.Method == "GET" {
		catalog, err := analytics.Catalog()
		if err != nil {
			fail(err)
			return
		}
		allowed := []analytics.Dataset{}
		for _, dataset := range catalog.Datasets {
			if (dataset.AuthorizationDomain == "operational" && auth.HasRole(r.Context(), auth.Operator)) || (dataset.AuthorizationDomain == "audit" && auth.HasRole(r.Context(), auth.PlatformAdmin)) {
				allowed = append(allowed, dataset)
			}
		}
		if len(allowed) == 0 {
			fail(investigation.ErrDenied)
			return
		}
		catalog.Datasets = allowed
		tasks := []string{}
		if h.Repository.Jobs.AssistantEnabled {
			for _, kind := range h.ModelTaskKinds {
				if kind == "data_query" || (auth.HasRole(r.Context(), auth.Operator) && (kind == "resource_troubleshooting" || kind == "incident_followup")) {
					tasks = append(tasks, kind)
				}
			}
		}
		sources := []analytics.SourceCapability{}
		network := []evidence.CapabilitySet{}
		sourceStatus := "cluster_not_selected"
		if cluster := r.URL.Query().Get("clusterUid"); cluster != "" {
			if !auth.HasRole(r.Context(), auth.Operator) {
				fail(investigation.ErrDenied)
				return
			}
			grant, err := (graph.Authorization{Pool: h.Repository.Pool}).Effective(r.Context(), actor.TenantID.String(), actor.Subject, cluster)
			if err != nil {
				fail(err)
				return
			}
			j := investigation.Job{TenantID: actor.TenantID, Subject: actor.Subject, Scope: grant, Assistant: &investigation.AssistantTask{GrantSnapshotDigest: graph.ScopeDigest(grant)}}
			details, err := h.SP04.AssistantCapabilityDetails(r.Context(), j)
			if err == nil {
				sources = details.Capabilities
				network = details.Network
			}
			sourceStatus = "available"
			if err != nil {
				sourceStatus = "unavailable"
				sources = []analytics.SourceCapability{}
			}
		}
		writeJSON(w, 200, map[string]any{"data": map[string]any{"enabled": h.Repository.Jobs.AssistantEnabled, "faultChainEnabled": h.Repository.FaultChainEnabled, "sourceCapabilities": sources, "networkCapabilities": network, "sourceCapabilityStatus": sourceStatus, "taskKinds": tasks, "catalog": catalog, "virtualization": "deferred", "runtimeAcceptance": "unverified"}, "requestId": actor.RequestID})
		return
	}
	if r.URL.Path == "/api/v1/assistant/legal-holds" {
		h.legalHold(w, r, actor)
		return
	}
	if r.URL.Path == "/api/v1/assistant/resources:interpret" {
		h.interpret(w, r, actor)
		return
	}
	if r.URL.Path == "/api/v1/assistant/resources:resolve" {
		h.resolveResource(w, r, actor)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/assistant/"), "/")
	if len(parts) == 1 && parts[0] == "conversations" {
		switch r.Method {
		case "POST":
			var q struct {
				Title                string               `json:"title"`
				Mode                 string               `json:"mode"`
				AuthorizationDomain  string               `json:"authorizationDomain"`
				ClusterUID           string               `json:"clusterUid"`
				NamespaceSelection   []string             `json:"namespaceSelection"`
				IncludeClusterScoped bool                 `json:"includeClusterScoped"`
				IncidentID           *uuid.UUID           `json:"incidentId,omitempty"`
				ResourceCanonicalIDs []string             `json:"resourceCanonicalIds"`
				Symptom              string               `json:"symptom"`
				TimeRange            *assistant.TimeRange `json:"timeRange,omitempty"`
				Timezone             string               `json:"timezone"`
			}
			if decodeInvestigation(r, &q) != nil {
				fail(investigation.ErrInvalid)
				return
			}
			if (q.AuthorizationDomain == "operational" && !auth.HasRole(r.Context(), auth.Operator)) || (q.AuthorizationDomain == "audit" && !auth.HasRole(r.Context(), auth.PlatformAdmin)) {
				fail(investigation.ErrDenied)
				return
			}
			c := assistant.Conversation{TenantID: actor.TenantID, OwnerSubject: actor.Subject, Title: q.Title, Mode: q.Mode, AuthorizationDomain: q.AuthorizationDomain, ClusterUID: q.ClusterUID, Binding: assistant.ConversationBinding{Timezone: q.Timezone, Selection: analytics.Selection{Namespaces: q.NamespaceSelection, IncludeClusterScoped: q.IncludeClusterScoped, CanonicalIDs: q.ResourceCanonicalIDs}}}
			if q.Mode == "data_query" {
				c.Binding.TimeRange = q.TimeRange
			}
			if q.IncidentID != nil {
				c.Binding.IncidentID = *q.IncidentID
			}
			if q.Mode == "troubleshoot" && c.Binding.IncidentID == uuid.Nil {
				if len(q.ResourceCanonicalIDs) != 1 || q.TimeRange == nil || h.SP04 == nil {
					fail(investigation.ErrInvalid)
					return
				}
				grant, err := (graph.Authorization{Pool: h.Repository.Pool}).Effective(r.Context(), actor.TenantID.String(), actor.Subject, q.ClusterUID)
				if err != nil {
					fail(err)
					return
				}
				route, err := (graph.Repository{Pool: h.Repository.Pool}).Load(r.Context(), actor.TenantID.String(), q.ClusterUID)
				if err != nil {
					fail(err)
					return
				}
				scope, err := analytics.RestrictScope(grant, c.Binding.Selection)
				if err != nil {
					fail(err)
					return
				}
				j := investigation.Job{TenantID: actor.TenantID, Subject: actor.Subject, Scope: scope, Assistant: &investigation.AssistantTask{GrantSnapshotDigest: graph.ScopeDigest(grant)}}
				resolution, err := (assistant.ResourceResolver{Authorization: graph.Authorization{Pool: h.Repository.Pool}, Graph: assistantGraphReader{h.SP04, j}}).Resolve(r.Context(), analytics.Binding{Subject: actor.Subject, Grant: grant, Selection: c.Binding.Selection, OwnerEpoch: route.Epoch}, assistant.ResourceSelector{CanonicalID: q.ResourceCanonicalIDs[0]})
				if err != nil {
					fail(err)
					return
				}
				rc, err := assistant.NewContext(resolution, q.Symptom, *q.TimeRange)
				if err != nil {
					fail(err)
					return
				}
				c.Binding.ResourceContext = &rc
			}
			out, err := h.Repository.Create(r.Context(), c, assistant.CreationKey{Key: r.Header.Get("Idempotency-Key"), RequestDigest: graph.ScopeDigest(q)})
			if err != nil {
				fail(err)
				return
			}
			writeJSON(w, 201, map[string]any{"data": out, "requestId": actor.RequestID})
		case "GET":
			limit := 20
			if v := r.URL.Query().Get("limit"); v != "" {
				var err error
				limit, err = strconv.Atoi(v)
				if err != nil {
					fail(investigation.ErrInvalid)
					return
				}
			}
			cursor, err := h.decodeCursor(r.URL.Query().Get("cursor"), actor, "assistant-list")
			if err != nil {
				fail(err)
				return
			}
			page, err := h.Repository.ListPage(r.Context(), actor.TenantID, actor.Subject, cursor.Before, cursor.BeforeID, limit)
			if err != nil {
				fail(err)
				return
			}
			next := ""
			if page.HasMore {
				next = h.cursor(actor, assistantCursor{Kind: "assistant-list", Before: page.Before, BeforeID: page.BeforeID})
			}
			writeJSON(w, 200, map[string]any{"data": page.Items, "meta": map[string]any{"nextCursor": next}, "requestId": actor.RequestID})
		default:
			writeSP04Error(w, 405, "INVALID_ARGUMENT", "method unavailable", false, actor.RequestID)
		}
		return
	}
	if len(parts) == 4 && parts[0] == "turns" && parts[2] == "source-steps" && r.Method == "GET" {
		turn, err := uuid.Parse(parts[1])
		if err != nil {
			fail(investigation.ErrInvalid)
			return
		}
		step, err := uuid.Parse(parts[3])
		if err != nil {
			fail(investigation.ErrInvalid)
			return
		}
		out, err := h.Repository.ResourceSourceStep(r.Context(), actor.TenantID, turn, step, actor.Subject)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, map[string]any{"data": out, "requestId": actor.RequestID})
		return
	}
	if len(parts) == 3 && parts[0] == "turns" && parts[2] == "fault-chain-context" && r.Method == "GET" {
		turn, err := uuid.Parse(parts[1])
		if err != nil {
			fail(investigation.ErrInvalid)
			return
		}
		out, err := h.Repository.FaultChainContext(r.Context(), actor.TenantID, turn, actor.Subject)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, map[string]any{"data": out, "requestId": actor.RequestID})
		return
	}
	if len(parts) == 3 && parts[0] == "turns" && parts[2] == "formal-references" && r.Method == "GET" {
		turn, err := uuid.Parse(parts[1])
		if err != nil {
			fail(investigation.ErrInvalid)
			return
		}
		index, err := strconv.Atoi(r.URL.Query().Get("index"))
		if err != nil {
			fail(investigation.ErrInvalid)
			return
		}
		out, err := h.Repository.FormalReference(r.Context(), actor.TenantID, turn, actor.Subject, index)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, map[string]any{"data": out, "requestId": actor.RequestID})
		return
	}
	if len(parts) == 2 && parts[0] == "turns" {
		cancel := strings.HasSuffix(parts[1], ":cancel")
		id, err := uuid.Parse(strings.TrimSuffix(parts[1], ":cancel"))
		if err != nil {
			fail(investigation.ErrInvalid)
			return
		}
		if cancel && r.Method == "POST" {
			if !assistantMutation(r) {
				fail(investigation.ErrInvalid)
				return
			}
			if err = h.Repository.Cancel(r.Context(), actor.TenantID, id, actor.Subject, r.Header.Get("Idempotency-Key")); err != nil {
				fail(err)
				return
			}
			writeJSON(w, 200, map[string]any{"data": map[string]any{"cancelled": true}, "requestId": actor.RequestID})
			return
		}
		if !cancel && r.Method == "GET" {
			out, err := h.Repository.TurnReadable(r.Context(), actor.TenantID, id, actor.Subject)
			if err != nil {
				fail(err)
				return
			}
			writeJSON(w, 200, map[string]any{"data": out, "requestId": actor.RequestID})
			return
		}
	}
	if len(parts) == 2 && parts[0] == "query-results" && r.Method == "GET" {
		id, err := uuid.Parse(parts[1])
		if err != nil {
			fail(investigation.ErrInvalid)
			return
		}
		out, err := h.Repository.QueryResult(r.Context(), actor.TenantID, id, actor.Subject)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, map[string]any{"data": out, "requestId": actor.RequestID})
		return
	}
	if len(parts) >= 2 && parts[0] == "conversations" {
		archive := strings.HasSuffix(parts[1], ":archive")
		id, err := uuid.Parse(strings.TrimSuffix(parts[1], ":archive"))
		if err != nil {
			fail(investigation.ErrInvalid)
			return
		}
		if len(parts) == 2 {
			if archive && r.Method == "POST" {
				if !assistantMutation(r) {
					fail(investigation.ErrInvalid)
					return
				}
				if err := h.Repository.Archive(r.Context(), actor.TenantID, id, actor.Subject); err != nil {
					fail(err)
					return
				}
				writeJSON(w, 200, map[string]any{"data": map[string]any{"archived": true}, "requestId": actor.RequestID})
				return
			}
			if !archive && r.Method == "GET" {
				out, err := h.Repository.GetReadable(r.Context(), actor.TenantID, id, actor.Subject)
				if err != nil {
					fail(err)
					return
				}
				writeJSON(w, 200, map[string]any{"data": out, "requestId": actor.RequestID})
				return
			}
		}
		if len(parts) == 3 && parts[2] == "binding" {
			h.rebind(w, r, actor, id)
			return
		}
		if len(parts) == 3 && (parts[2] == "messages" || parts[2] == "fault-chain-investigations") {
			if r.Method == "POST" {
				if len(r.Header.Values("Idempotency-Key")) != 1 || !validIdempotencyKey(r.Header.Get("Idempotency-Key")) {
					fail(investigation.ErrInvalid)
					return
				}
				var q assistant.SubmitMessage
				if decodeInvestigation(r, &q) != nil {
					fail(investigation.ErrInvalid)
					return
				}
				if parts[2] == "fault-chain-investigations" && q.FaultChainRequest == nil {
					fail(investigation.ErrInvalid)
					return
				}
				out, err := h.Repository.Submit(r.Context(), actor.TenantID, id, actor.Subject, r.Header.Get("Idempotency-Key"), q)
				if err != nil {
					fail(err)
					return
				}
				writeJSON(w, 202, map[string]any{"data": out, "requestId": actor.RequestID})
				return
			}
			if r.Method == "GET" && parts[2] == "messages" {
				cursor, err := h.decodeCursor(r.URL.Query().Get("cursor"), actor, "assistant-messages")
				if err != nil {
					fail(err)
					return
				}
				conversation, err := h.Repository.Get(r.Context(), actor.TenantID, id, actor.Subject)
				if err != nil {
					fail(err)
					return
				}
				digest := graph.ScopeDigest(conversation.Binding.ExecutionScope)
				if r.URL.Query().Get("cursor") != "" && (cursor.ConversationID != id || cursor.ScopeDigest != digest || cursor.Sequence < 1) {
					fail(investigation.ErrDenied)
					return
				}
				limit := 100
				if value := r.URL.Query().Get("limit"); value != "" {
					limit, err = strconv.Atoi(value)
					if err != nil {
						fail(investigation.ErrInvalid)
						return
					}
				}
				out, err := h.Repository.MessagePage(r.Context(), actor.TenantID, id, actor.Subject, cursor.Sequence, limit)
				if err != nil {
					fail(err)
					return
				}
				next := ""
				if len(out) == limit {
					next = h.cursor(actor, assistantCursor{Kind: "assistant-messages", ConversationID: id, ScopeDigest: digest, Sequence: out[0].Sequence})
				}
				writeJSON(w, 200, map[string]any{"data": out, "requestId": actor.RequestID, "meta": map[string]any{"nextCursor": next}})
				return
			}
		}
		if len(parts) == 3 && parts[2] == "events" && r.Method == "GET" {
			h.stream(w, r, actor, id)
			return
		}
	}
	writeSP04Error(w, 404, "NOT_FOUND", "assistant endpoint unavailable", false, actor.RequestID)
}
func assistantMutation(r *http.Request) bool {
	var empty map[string]any
	return decodeInvestigation(r, &empty) == nil && len(empty) == 0 && len(r.Header.Values("Idempotency-Key")) == 1 && validIdempotencyKey(r.Header.Get("Idempotency-Key"))
}
func (h *AssistantHandlers) stream(w http.ResponseWriter, r *http.Request, actor auth.RequestContext, id uuid.UUID) {
	cursor, err := h.decodeCursor(r.Header.Get("Last-Event-ID"), actor, "assistant-events")
	if err != nil {
		assistantError(w, err, actor.RequestID)
		return
	}
	if cursor.ConversationID != uuid.Nil && cursor.ConversationID != id {
		assistantError(w, investigation.ErrDenied, actor.RequestID)
		return
	}
	c, events, err := h.Repository.Events(r.Context(), actor.TenantID, id, actor.Subject, cursor.Sequence)
	if err != nil {
		assistantError(w, err, actor.RequestID)
		return
	}
	digest := graph.ScopeDigest(c.Binding)
	if cursor.ScopeDigest != "" && cursor.ScopeDigest != digest {
		assistantError(w, investigation.ErrDenied, actor.RequestID)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	controller := http.NewResponseController(w)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	after := cursor.Sequence
	for {
		for _, e := range events {
			if ctx.Err() != nil || (!actor.TokenExpiresAt.IsZero() && !actor.TokenExpiresAt.After(time.Now())) {
				return
			}
			if _, _, err = h.Repository.Events(ctx, actor.TenantID, id, actor.Subject, after); err != nil {
				return
			}
			token := h.cursor(actor, assistantCursor{Kind: "assistant-events", ConversationID: id, ScopeDigest: digest, Sequence: e.EventID})
			_ = controller.SetWriteDeadline(time.Now().Add(3 * time.Second))
			if _, err = fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", token, e.Type, rawJSON(e)); err != nil || controller.Flush() != nil {
				return
			}
			after = e.EventID
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if !actor.TokenExpiresAt.IsZero() && !actor.TokenExpiresAt.After(time.Now()) {
			return
		}
		_, events, err = h.Repository.Events(ctx, actor.TenantID, id, actor.Subject, after)
		if err != nil {
			return
		}
	}
}

func decodeAssistantCursor(key ed25519.PublicKey, token string, out *assistantCursor) error {
	p := strings.Split(token, ".")
	if len(p) != 2 || len(token) > 4096 {
		return investigation.ErrInvalid
	}
	b, e1 := base64.RawURLEncoding.DecodeString(p[0])
	sig, e2 := base64.RawURLEncoding.DecodeString(p[1])
	if e1 != nil || e2 != nil || !ed25519.Verify(key, b, sig) || json.Unmarshal(b, out) != nil {
		return investigation.ErrDenied
	}
	return nil
}
