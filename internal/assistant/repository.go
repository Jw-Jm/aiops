package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/analytics"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
	"ops-platform/internal/investigation/tools"
	"ops-platform/internal/persistence"
)

type Admission struct {
	PolicyVersion       string
	ModelProfileVersion string
	Budget              investigation.Budget
}
type Repository struct {
	FaultChainEnabled bool
	Pool              persistence.TxBeginner
	Jobs              investigation.Repository
	// Admit applies the existing signed policy and freezes the admitted model profile.
	// Missing admission denies submission; it never starts a private model path.
	Admit func(context.Context, Conversation, string) (Admission, error)
}

func raw(v any) []byte { b, _ := json.Marshal(v); return b }

const conversationColumns = `tenant_id,conversation_id,owner_subject,title,mode,authorization_domain,cluster_uid,binding,revision,archived_at,created_at,updated_at`

func loadConversation(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, subject string, lock bool) (Conversation, error) {
	var c Conversation
	var binding []byte
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	err := tx.QueryRow(ctx, `SELECT `+conversationColumns+` FROM investigation.assistant_conversations WHERE tenant_id=$1 AND conversation_id=$2 AND owner_subject=$3`+suffix, tenant, id, subject).Scan(&c.TenantID, &c.ConversationID, &c.OwnerSubject, &c.Title, &c.Mode, &c.AuthorizationDomain, &c.ClusterUID, &binding, &c.Revision, &c.ArchivedAt, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return c, err
	}
	if json.Unmarshal(binding, &c.Binding) != nil {
		return c, investigation.ErrInvalid
	}
	return c, nil
}
func authorizeConversation(ctx context.Context, tx pgx.Tx, c Conversation) (graph.Scope, error) {
	var grant graph.Scope
	var err error
	if c.AuthorizationDomain == "audit" {
		grant, err = investigation.AuditScopeTx(ctx, tx, c.TenantID, c.OwnerSubject)
	} else {
		grant, err = graph.EffectiveTx(ctx, tx, c.TenantID.String(), c.OwnerSubject, c.ClusterUID, true)
	}
	if err != nil {
		return grant, investigation.ErrDenied
	}
	// Rebinding is explicit and stays within this frozen grant. A revoked or
	// modified grant invalidates old prompt history rather than leaking its text.
	if graph.ScopeDigest(grant) != graph.ScopeDigest(c.Binding.GrantSnapshot) || !investigation.ScopeSubset(grant, c.Binding.ExecutionScope) {
		return grant, investigation.ErrDenied
	}
	if err := authorizeFrozenBinding(ctx, tx, c); err != nil {
		return grant, err
	}
	return grant, nil
}

// authorizeFrozenBinding re-checks the frozen resource and incident references
// against current rows. It is shared by same-owner and mapped-owner reads: the
// frozen evidence is verified, never rewritten.
func authorizeFrozenBinding(ctx context.Context, tx pgx.Tx, c Conversation) error {
	if c.Binding.ResourceContext != nil {
		for _, candidate := range c.Binding.ResourceContext.Resources {
			var ns string
			if tx.QueryRow(ctx, `SELECT namespace FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2 AND deleted_at IS NULL`, c.TenantID, candidate.CanonicalID).Scan(&ns) != nil || !c.Binding.ExecutionScope.Allows(candidate.CanonicalID, ns) {
				return investigation.ErrDenied
			}
		}
	}
	if c.Binding.IncidentID != uuid.Nil {
		var cluster string
		var ns string
		if tx.QueryRow(ctx, `SELECT cluster_uid,namespace FROM incident.records WHERE tenant_id=$1 AND incident_id=$2`, c.TenantID, c.Binding.IncidentID).Scan(&cluster, &ns) != nil || cluster != c.ClusterUID {
			return investigation.ErrDenied
		}
		if (ns == "" && !c.Binding.ExecutionScope.ClusterScoped) || (ns != "" && !slices.Contains(c.Binding.ExecutionScope.Namespaces, ns)) {
			return investigation.ErrDenied
		}
		rows, err := tx.Query(ctx, `SELECT f.cluster_uid,f.namespace,COALESCE(f.resource_canonical_id,'') FROM incident.finding_links l JOIN finding.records f USING(tenant_id,finding_id) WHERE l.tenant_id=$1 AND l.incident_id=$2`, c.TenantID, c.Binding.IncidentID)
		if err != nil {
			return investigation.ErrDenied
		}
		for rows.Next() {
			var findingCluster, namespace, canonicalID string
			if rows.Scan(&findingCluster, &namespace, &canonicalID) != nil || findingCluster != c.ClusterUID || !c.Binding.ExecutionScope.Allows(canonicalID, namespace) {
				rows.Close()
				return investigation.ErrDenied
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return investigation.ErrDenied
		}
	}
	return nil
}

type CreationKey struct{ Key, RequestDigest string }

func (r Repository) Create(ctx context.Context, c Conversation, keys ...CreationKey) (Conversation, error) {
	if len(keys) > 1 || (len(keys) == 1 && (keys[0].Key == "" || len(keys[0].Key) > 128 || keys[0].RequestDigest == "")) {
		return c, investigation.ErrInvalid
	}
	if c.TenantID == uuid.Nil || !analytics.ValidText(c.OwnerSubject, 512) || (c.Mode != "data_query" && c.Mode != "troubleshoot") || !analytics.ValidText(c.Title, 160) {
		return c, investigation.ErrInvalid
	}
	if c.Binding.Timezone == "" || c.Binding.Timezone == "Local" {
		return c, investigation.ErrInvalid
	}
	if _, err := time.LoadLocation(c.Binding.Timezone); err != nil {
		return c, investigation.ErrInvalid
	}
	if w := c.Binding.TimeRange; w != nil {
		if c.Mode != "data_query" || w.From.IsZero() || !w.To.After(w.From) || w.To.Sub(w.From) > 31*24*time.Hour || w.To.After(time.Now().Add(time.Minute)) || w.Timezone != c.Binding.Timezone {
			return c, investigation.ErrInvalid
		}
		copy := *w
		copy.From = copy.From.UTC()
		copy.To = copy.To.UTC()
		c.Binding.TimeRange = &copy
	}
	if c.AuthorizationDomain == "audit" {
		if c.Mode != "data_query" || c.ClusterUID != "" || c.Binding.IncidentID != uuid.Nil || c.Binding.ResourceContext != nil || len(c.Binding.Selection.Namespaces) > 0 || len(c.Binding.Selection.CanonicalIDs) > 0 || c.Binding.Selection.IncludeClusterScoped {
			return c, investigation.ErrInvalid
		}
	} else if c.AuthorizationDomain != "operational" || c.ClusterUID == "" {
		return c, investigation.ErrInvalid
	}
	if c.Mode == "data_query" && (c.Binding.IncidentID != uuid.Nil || c.Binding.ResourceContext != nil) {
		return c, investigation.ErrInvalid
	}
	if c.Mode == "troubleshoot" && ((c.Binding.IncidentID == uuid.Nil) == (c.Binding.ResourceContext == nil)) {
		return c, investigation.ErrInvalid
	}
	if c.Binding.ResourceContext != nil {
		rc := c.Binding.ResourceContext
		if ValidateContext(*rc) != nil || rc.TenantID != c.TenantID.String() || rc.OwnerSubject != c.OwnerSubject || rc.ClusterUID != c.ClusterUID {
			return c, investigation.ErrInvalid
		}
		c.Binding.ExecutionScope = rc.ExecutionScope
	}
	c.Title = tools.RedactText(c.Title)
	c.ConversationID = uuid.Must(uuid.NewV7())
	c.Revision = 1
	err := persistence.WithTenantTx(ctx, r.Pool, c.TenantID, func(tx pgx.Tx) error {
		var err error
		if len(keys) == 1 {
			key := keys[0]
			if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, c.TenantID.String()+"|assistant-create|"+c.OwnerSubject+"|"+key.Key); err != nil {
				return err
			}
			var id uuid.UUID
			var digest string
			err = tx.QueryRow(ctx, `SELECT conversation_id,request_digest FROM investigation.assistant_creation_keys WHERE tenant_id=$1 AND owner_subject=$2 AND idempotency_key=$3`, c.TenantID, c.OwnerSubject, key.Key).Scan(&id, &digest)
			if err == nil {
				if digest != key.RequestDigest {
					return investigation.ErrConflict
				}
				c, err = loadConversation(ctx, tx, c.TenantID, id, c.OwnerSubject, false)
				if err != nil {
					return err
				}
				_, err = authorizeConversation(ctx, tx, c)
				return err
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		if c.AuthorizationDomain == "audit" {
			c.Binding.GrantSnapshot, err = investigation.AuditScopeTx(ctx, tx, c.TenantID, c.OwnerSubject)
			c.Binding.ExecutionScope = c.Binding.GrantSnapshot
		} else {
			c.Binding.GrantSnapshot, err = graph.EffectiveTx(ctx, tx, c.TenantID.String(), c.OwnerSubject, c.ClusterUID, true)
			if err == nil {
				selection := c.Binding.Selection
				if c.Binding.ResourceContext != nil {
					// The resolved UID is an investigation anchor. Dependency/impact reads
					// remain inside the explicitly selected namespaces and current grant.
					selection.CanonicalIDs = nil
					if len(selection.Namespaces) == 0 {
						ns := c.Binding.ResourceContext.Resources[0].Namespace
						if ns != "" {
							selection.Namespaces = []string{ns}
						}
					}
					if c.Binding.ResourceContext.Resources[0].Namespace == "" {
						selection.IncludeClusterScoped = true
					}
					c.Binding.Selection = selection
				}
				c.Binding.ExecutionScope, err = analytics.RestrictScope(c.Binding.GrantSnapshot, selection)
			}
		}
		if err != nil {
			return investigation.ErrDenied
		}
		if _, err = authorizeConversation(ctx, tx, c); err != nil {
			return err
		}
		if err = tx.QueryRow(ctx, `INSERT INTO investigation.assistant_conversations(tenant_id,conversation_id,owner_subject,title,mode,authorization_domain,cluster_uid,binding) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING created_at,updated_at`, c.TenantID, c.ConversationID, c.OwnerSubject, c.Title, c.Mode, c.AuthorizationDomain, c.ClusterUID, raw(c.Binding)).Scan(&c.CreatedAt, &c.UpdatedAt); err != nil {
			return err
		}
		if len(keys) == 1 {
			_, err = tx.Exec(ctx, `INSERT INTO investigation.assistant_creation_keys(tenant_id,owner_subject,idempotency_key,request_digest,conversation_id) VALUES($1,$2,$3,$4,$5)`, c.TenantID, c.OwnerSubject, keys[0].Key, keys[0].RequestDigest, c.ConversationID)
		}
		return err
	})
	return c, err
}
func (r Repository) Get(ctx context.Context, tenant, id uuid.UUID, subject string) (Conversation, error) {
	return r.get(ctx, tenant, id, subject, false)
}

// GetReadable additionally serves history still owned by an explicitly mapped
// pre-native subject. It is read-only: every write path keeps using Get, so a
// mapped account can never append to, archive or rebind a historical owner's
// conversation.
func (r Repository) GetReadable(ctx context.Context, tenant, id uuid.UUID, subject string) (Conversation, error) {
	return r.get(ctx, tenant, id, subject, true)
}

func (r Repository) get(ctx context.Context, tenant, id uuid.UUID, subject string, allowMapped bool) (Conversation, error) {
	var c Conversation
	err := persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		var err error
		read := investigation.HistoricalRead{OwnerSubject: subject, CurrentSubject: subject}
		var rs readSubjects
		if allowMapped {
			if rs, err = newReadSubjects(ctx, tx, tenant, subject); err != nil {
				return err
			}
			c, read, err = loadReadableConversation(ctx, tx, tenant, id, rs, false)
		} else {
			c, err = loadConversation(ctx, tx, tenant, id, subject, false)
			read.OwnerSubject = c.OwnerSubject
		}
		if err != nil {
			return err
		}
		if _, err = authorizeRead(ctx, tx, c, read); err != nil {
			return err
		}
		if err = rs.recordAudit(ctx, tx, tenant, c, read); err != nil {
			return err
		}
		var turnID uuid.UUID
		err = tx.QueryRow(ctx, `SELECT turn_id FROM investigation.assistant_turns WHERE tenant_id=$1 AND conversation_id=$2 ORDER BY created_at DESC,turn_id DESC LIMIT 1`, tenant, id).Scan(&turnID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		turn, err := loadTurn(ctx, tx, tenant, turnID)
		if err != nil {
			return err
		}
		if _, err := investigation.AssistantReadJobTxAs(ctx, tx, c.TenantID, read, c.ConversationID, turn.TurnID, c.Binding.ExecutionScope); err != nil {
			if errors.Is(err, investigation.ErrDenied) {
				return nil
			}
			return err
		}
		if len(turn.Answer) > 0 {
			if err := authorizeTurnAnswer(ctx, tx, c, turn.TurnID, turn.Answer, read); err != nil {
				if !errors.Is(err, investigation.ErrResultExpired) {
					return err
				}
				turn.Answer = nil
				turn.ErrorCode = "RESULT_EXPIRED"
			}
		}
		c.ActiveTurn = &turn
		return nil
	})
	return c, err
}
func taskKind(c Conversation) string {
	if c.Mode == "data_query" {
		return "data_query"
	}
	if c.Binding.IncidentID != uuid.Nil {
		return "incident_followup"
	}
	return "resource_troubleshooting"
}
func authorizeTurnAnswer(ctx context.Context, tx pgx.Tx, c Conversation, turn uuid.UUID, answer json.RawMessage, read investigation.HistoricalRead) error {
	// The proven mapped read travels with the transaction context so nested
	// read-only checks (fault-chain context and its source steps) re-prove the
	// same Job instead of falling back to the frozen legacy subject.
	readCtx, j, err := investigation.AuthorizeHistoricalReadTx(ctx, tx, c.TenantID, read, c.ConversationID, turn, c.Binding.ExecutionScope)
	if err != nil {
		return err
	}
	if err = investigation.AssistantAnswerRetainedTx(ctx, tx, c.TenantID, c.ConversationID, turn); err != nil {
		return err
	}
	return investigation.AuthorizeAssistantResultTx(readCtx, tx, j, answer)
}

func messages(ctx context.Context, tx pgx.Tx, c Conversation, read investigation.HistoricalRead, limit int, cursors ...int64) ([]Message, error) {
	out := []Message{}
	before := int64(9223372036854775807)
	if len(cursors) > 0 && cursors[0] > 0 {
		before = cursors[0]
	}
	rows, err := tx.Query(ctx, `SELECT message_id,COALESCE(turn_id,'00000000-0000-0000-0000-000000000000'::uuid),sequence,role,content,created_at FROM investigation.assistant_messages WHERE tenant_id=$1 AND conversation_id=$2 AND (retain_until>clock_timestamp() OR legal_hold) AND sequence<$4 ORDER BY sequence DESC LIMIT $3`, c.TenantID, c.ConversationID, limit, before)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var m Message
		if err = rows.Scan(&m.MessageID, &m.TurnID, &m.Sequence, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	filtered := []Message{}
	deniedTurns := map[uuid.UUID]bool{}
	for _, m := range out {
		if _, err := investigation.AssistantReadJobTxAs(ctx, tx, c.TenantID, read, c.ConversationID, m.TurnID, c.Binding.ExecutionScope); err != nil {
			if errors.Is(err, investigation.ErrDenied) {
				deniedTurns[m.TurnID] = true
				continue
			}
			return nil, err
		}
		if m.Role == "assistant" {
			if err := authorizeTurnAnswer(ctx, tx, c, m.TurnID, m.Content, read); err != nil {
				if errors.Is(err, investigation.ErrDenied) {
					deniedTurns[m.TurnID] = true
					continue
				}
				return nil, err
			}
		}
	}
	for _, m := range out {
		if !deniedTurns[m.TurnID] {
			filtered = append(filtered, m)
		}
	}
	out = filtered
	return out, nil
}
func (r Repository) Messages(ctx context.Context, tenant, id uuid.UUID, subject string) ([]Message, error) {
	return r.MessagePage(ctx, tenant, id, subject, 0, 100)
}
func (r Repository) MessagePage(ctx context.Context, tenant, id uuid.UUID, subject string, before int64, limit int) ([]Message, error) {
	if before < 0 || limit < 1 || limit > 100 {
		return nil, investigation.ErrInvalid
	}
	var out []Message
	err := persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		rs, err := newReadSubjects(ctx, tx, tenant, subject)
		if err != nil {
			return err
		}
		c, read, err := loadReadableConversation(ctx, tx, tenant, id, rs, false)
		if err != nil {
			return err
		}
		if _, err = authorizeRead(ctx, tx, c, read); err != nil {
			return err
		}
		if err = rs.recordAudit(ctx, tx, tenant, c, read); err != nil {
			return err
		}
		out, err = messages(ctx, tx, c, read, limit, before)
		return err
	})
	return out, err
}

const turnColumns = `turn_id,conversation_id,job_id,parent_turn_id,state,error_code,answer,created_at,base_conversation_revision+1`

func loadTurn(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Turn, error) {
	var out Turn
	err := tx.QueryRow(ctx, `SELECT `+turnColumns+` FROM investigation.assistant_turns WHERE tenant_id=$1 AND turn_id=$2`, tenant, id).Scan(&out.TurnID, &out.ConversationID, &out.JobID, &out.ParentTurnID, &out.State, &out.ErrorCode, &out.Answer, &out.CreatedAt, &out.ConversationRevision)
	return out, err
}
func (r Repository) Submit(ctx context.Context, tenant, id uuid.UUID, subject, key string, q SubmitMessage) (Turn, error) {
	var out Turn
	if key == "" || len(key) > 128 || strings.TrimSpace(q.Message) == "" || !utf8.ValidString(q.Message) || utf8.RuneCountInString(q.Message) > 4000 || len(q.Message) > 16<<10 || q.ExpectedConversationRevision < 1 || r.Admit == nil || !r.Jobs.AssistantEnabled {
		return out, investigation.ErrInvalid
	}
	c, err := r.Get(ctx, tenant, id, subject)
	if err != nil {
		return out, err
	}
	if q.FaultChainRequest != nil && (!r.FaultChainEnabled || c.Mode != "troubleshoot" || c.AuthorizationDomain != "operational" || !q.FaultChainRequest.Valid(c.Binding.ExecutionScope, c.Binding.Timezone, time.Now())) {
		return out, investigation.ErrInvalid
	}
	admission, err := r.Admit(ctx, c, taskKind(c))
	if err != nil {
		return out, err
	}
	if !admission.Budget.Valid() || admission.ModelProfileVersion == "" {
		return out, investigation.ErrDenied
	}
	digest := graph.ScopeDigest(struct {
		ID    uuid.UUID
		Input SubmitMessage
	}{id, q})
	err = persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, tenant.String()+"|assistant|"+subject+"|"+key); err != nil {
			return err
		}
		var previous uuid.UUID
		var priorDigest string
		err := tx.QueryRow(ctx, `SELECT turn_id,request_digest FROM investigation.assistant_request_keys WHERE tenant_id=$1 AND owner_subject=$2 AND idempotency_key=$3`, tenant, subject, key).Scan(&previous, &priorDigest)
		if err == nil {
			if priorDigest != digest {
				return investigation.ErrConflict
			}
			out, err = loadTurn(ctx, tx, tenant, previous)
			if err != nil {
				return err
			}
			c, err = loadConversation(ctx, tx, tenant, id, subject, false)
			if err != nil {
				return err
			}
			_, err = authorizeConversation(ctx, tx, c)
			if err != nil {
				return err
			}
			if len(out.Answer) > 0 {
				return authorizeTurnAnswer(ctx, tx, c, out.TurnID, out.Answer, ownerRead(c, c.OwnerSubject))
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		c, err = loadConversation(ctx, tx, tenant, id, subject, true)
		if err != nil {
			return err
		}
		grant, err := authorizeConversation(ctx, tx, c)
		if err != nil {
			return err
		}
		if c.ArchivedAt != nil {
			return ErrArchived
		}
		if c.Revision != q.ExpectedConversationRevision {
			return ErrStale
		}
		var active bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM investigation.assistant_turns WHERE tenant_id=$1 AND conversation_id=$2 AND state IN('queued','running'))`, tenant, id).Scan(&active); err != nil {
			return err
		}
		if active {
			return ErrBusy
		}
		for _, parent := range []*uuid.UUID{q.ParentTurnID, q.ClarificationID} {
			if parent == nil {
				continue
			}
			var state string
			if tx.QueryRow(ctx, `SELECT state FROM investigation.assistant_turns WHERE tenant_id=$1 AND turn_id=$2 AND conversation_id=$3`, tenant, *parent, id).Scan(&state) != nil {
				return investigation.ErrInvalid
			}
			if parent == q.ClarificationID && state != "awaiting_user" {
				return investigation.ErrInvalid
			}
		}
		var ready bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM investigation.assistant_worker_capabilities WHERE tenant_id=$1 AND model_profile_version=$2 AND catalog_version='assistant-tools/v2' AND expires_at>clock_timestamp())`, tenant, admission.ModelProfileVersion).Scan(&ready); err != nil {
			return err
		}
		if !ready {
			return investigation.ErrNoJob
		}
		history, err := messages(ctx, tx, c, ownerRead(c, c.OwnerSubject), 12)
		if err != nil {
			return err
		}
		history, err = prepareHistory(history)
		if err != nil {
			return err
		}
		referenceResolution, err := ResolveReference(q.Message, q.ReferentCanonicalID, c.Binding, history)
		if err != nil {
			return err
		}
		requestedAt := time.Now().UTC()
		requestedWindow, err := ResolveTimeWindow(q.Message, requestedAt, c.Binding.Timezone)
		if err != nil {
			return err
		}
		if c.Mode == "data_query" && c.Binding.TimeRange != nil {
			window := c.Binding.TimeRange
			if requestedWindow != nil && (!requestedWindow.From.Equal(window.From) || !requestedWindow.To.Equal(window.To)) {
				return ErrStale
			}
			requestedWindow = &analytics.QueryTimeRange{From: window.From, To: window.To, Timezone: window.Timezone}
		}
		if c.Mode == "data_query" && requestedWindow == nil {
			// Inherit only an explicitly frozen prior turn window, already authorized
			// through the same retained history. "Now" never inherits old observations.
			if !strings.Contains(q.Message, "现在") && !strings.Contains(strings.ToLower(q.Message), "now") {
				for i := len(history) - 1; i >= 0; i-- {
					if history[i].Role != "assistant" {
						continue
					}
					var priorInput []byte
					if tx.QueryRow(ctx, `SELECT input FROM investigation.assistant_turns WHERE tenant_id=$1 AND turn_id=$2`, tenant, history[i].TurnID).Scan(&priorInput) != nil {
						return investigation.ErrDenied
					}
					var previous TurnInput
					if json.Unmarshal(priorInput, &previous) != nil {
						return investigation.ErrInvalid
					}
					requestedWindow = previous.RequestedTimeRange
					break
				}
			}
		}
		if q.FaultChainRequest != nil {
			if !q.FaultChainRequest.Valid(c.Binding.ExecutionScope, c.Binding.Timezone, requestedAt) {
				return investigation.ErrDenied
			}
			if q.FaultChainRequest.ReadOnlyCheck != nil {
				if err := validateFaultChainCheckTx(ctx, tx, c, q); err != nil {
					return err
				}
			}
			requestedWindow = &q.FaultChainRequest.Fault
		}
		input := TurnInput{FaultChainRequest: q.FaultChainRequest, RequestedTimeRange: requestedWindow, ReferenceResolution: referenceResolution, SchemaVersion: "assistant-input/v1", RequestedAt: requestedAt, Message: tools.RedactText(q.Message), Binding: c.Binding, History: history, HistoryManifestDigest: graph.ScopeDigest(history), ParentTurnID: q.ParentTurnID, ClarificationID: q.ClarificationID}
		if q.FaultChainRequest == nil && c.Mode == "troubleshoot" && !strings.Contains(q.Message, "现在") && !strings.Contains(strings.ToLower(q.Message), "now") {
			for i := len(history) - 1; i >= 0; i-- {
				if history[i].Role != "assistant" {
					continue
				}
				var priorRaw []byte
				var previous TurnInput
				if tx.QueryRow(ctx, `SELECT input FROM investigation.assistant_turns WHERE tenant_id=$1 AND turn_id=$2`, tenant, history[i].TurnID).Scan(&priorRaw) != nil || json.Unmarshal(priorRaw, &previous) != nil {
					return investigation.ErrDenied
				}
				frozen := previous.FaultChainRequest
				if frozen == nil {
					frozen = previous.FrozenCorrelationRequest
				}
				if frozen != nil && graph.ScopeDigest(previous.Binding) == graph.ScopeDigest(c.Binding) && (q.ReferentCanonicalID == "" || q.ReferentCanonicalID == frozen.TargetCanonicalID) && (requestedWindow == nil || *requestedWindow == frozen.Fault) && frozen.Valid(c.Binding.ExecutionScope, c.Binding.Timezone, requestedAt) {
					copy := *frozen
					copy.ReadOnlyCheck = nil
					input.FrozenCorrelationRequest = &copy
					input.RequestedTimeRange = &copy.Fault
				}
				break
			}
		}
		inputRaw := raw(input)
		if len(inputRaw) > 48<<10 {
			return investigation.ErrBudget
		}
		out = Turn{TurnID: uuid.Must(uuid.NewV7()), ConversationID: id, JobID: uuid.Must(uuid.NewV7()), ParentTurnID: q.ParentTurnID, State: "queued", ConversationRevision: c.Revision + 1}
		defs, err := tools.CatalogFor("investigation-job/v3", taskKind(c), c.AuthorizationDomain, "assistant-tools/v2")
		if err != nil {
			return err
		}
		j := investigation.Job{SchemaVersion: "investigation-job/v3", TenantID: tenant, JobID: out.JobID, IncidentID: c.Binding.IncidentID, Subject: subject, Scope: c.Binding.ExecutionScope, EffectiveScopeDigest: graph.ScopeDigest(c.Binding.ExecutionScope), PolicyVersion: admission.PolicyVersion, ToolCatalogDigest: tools.Digest(defs), Budget: admission.Budget, ExpiresAt: time.Now().UTC().Add(time.Duration(admission.Budget.DurationSeconds) * time.Second)}
		if j.IncidentID != uuid.Nil {
			if err := tx.QueryRow(ctx, `SELECT revision FROM incident.records WHERE tenant_id=$1 AND incident_id=$2 FOR SHARE`, tenant, j.IncidentID).Scan(&j.TriggerRevision); err != nil {
				return err
			}
		}
		datasetCatalog, err := analytics.Catalog()
		if err != nil {
			return err
		}
		j.Assistant = &investigation.AssistantTask{DatasetCatalogVersion: datasetCatalog.Version, DatasetCatalogDigest: graph.ScopeDigest(datasetCatalog), TaskKind: taskKind(c), AuthorizationDomain: c.AuthorizationDomain, ConversationID: id, TurnID: out.TurnID, InputDigest: graph.ScopeDigest(input), ContextDigest: graph.ScopeDigest(c.Binding), GrantSnapshot: grant, GrantSnapshotDigest: graph.ScopeDigest(grant), CatalogVersion: "assistant-tools/v2", PromptVersion: "assistant/v1", ModelProfileVersion: admission.ModelProfileVersion, ResultSchemaVersion: "assistant-answer/v1"}
		// Freeze the exact identity source and version the grant digest was
		// computed from, for audit and business jobs alike. The Worker re-proves
		// this source later without a browser session and cannot silently switch
		// identity systems or accept a re-granted role.
		authority := investigation.AuditAuthorityFromContext(ctx)
		j.Assistant.AuthorizationSource = authority.Source
		j.Assistant.AuthorizationUserID = authority.UserID
		j.Assistant.AuthorizationVersion = authority.AuthorizationVersion
		if q.FaultChainRequest != nil {
			j.Assistant.ExecutionMode = "evidence_only"
			j.Assistant.FaultChainRequestDigest = graph.ScopeDigest(*q.FaultChainRequest)
			j.Budget.ModelRequests = 0
			j.Budget.InputTokens = 0
			j.Budget.OutputTokens = 0
			j.Budget.DurationSeconds = min(j.Budget.DurationSeconds, 60)
			j.ExpiresAt = requestedAt.Add(time.Duration(j.Budget.DurationSeconds) * time.Second)
		}
		mid := uuid.Must(uuid.NewV7())
		if err = investigation.CreateAssistantJobTx(ctx, tx, j); err != nil {
			return err
		}
		if err = tx.QueryRow(ctx, `INSERT INTO investigation.assistant_turns(tenant_id,turn_id,conversation_id,job_id,parent_turn_id,user_message_id,input,input_digest,context_digest,state,base_conversation_revision) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'queued',$10) RETURNING created_at`, tenant, out.TurnID, id, out.JobID, q.ParentTurnID, mid, inputRaw, j.Assistant.InputDigest, j.Assistant.ContextDigest, c.Revision).Scan(&out.CreatedAt); err != nil {
			return err
		}
		if err = investigation.CheckAssistantJobTx(ctx, tx, j); err != nil {
			return err
		}
		var sequence int64
		if err = tx.QueryRow(ctx, `SELECT COALESCE(max(sequence),0)+1 FROM investigation.assistant_messages WHERE tenant_id=$1 AND conversation_id=$2`, tenant, id).Scan(&sequence); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO investigation.assistant_messages(tenant_id,message_id,conversation_id,turn_id,sequence,role,content) VALUES($1,$2,$3,$4,$5,'user',$6)`, tenant, mid, id, out.TurnID, sequence, raw(map[string]string{"text": tools.RedactText(q.Message)})); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO investigation.assistant_request_keys(tenant_id,owner_subject,idempotency_key,request_digest,conversation_id,turn_id) VALUES($1,$2,$3,$4,$5,$6)`, tenant, subject, key, digest, id, out.TurnID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE investigation.assistant_conversations SET revision=revision+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND conversation_id=$2`, tenant, id); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO investigation.assistant_events(tenant_id,conversation_id,turn_id,type,payload) VALUES($1,$2,$3,'turn.accepted',$4)`, tenant, id, out.TurnID, raw(out))
		return err
	})
	return out, err
}
func (r Repository) Turn(ctx context.Context, tenant, id uuid.UUID, subject string) (Turn, error) {
	return r.turn(ctx, tenant, id, subject, false)
}

// TurnReadable serves a turn of a conversation owned by an explicitly mapped
// pre-native subject. Cancel keeps the strict Turn path.
func (r Repository) TurnReadable(ctx context.Context, tenant, id uuid.UUID, subject string) (Turn, error) {
	return r.turn(ctx, tenant, id, subject, true)
}

func (r Repository) turn(ctx context.Context, tenant, id uuid.UUID, subject string, allowMapped bool) (Turn, error) {
	var out Turn
	err := persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		var err error
		out, err = loadTurn(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		read := investigation.HistoricalRead{OwnerSubject: subject, CurrentSubject: subject}
		var c Conversation
		var rs readSubjects
		if allowMapped {
			if rs, err = newReadSubjects(ctx, tx, tenant, subject); err != nil {
				return err
			}
			c, read, err = loadReadableConversation(ctx, tx, tenant, out.ConversationID, rs, false)
		} else {
			c, err = loadConversation(ctx, tx, tenant, out.ConversationID, subject, false)
			read.OwnerSubject = c.OwnerSubject
		}
		if err != nil {
			return err
		}
		if _, err = authorizeRead(ctx, tx, c, read); err != nil {
			return err
		}
		if err = rs.recordAudit(ctx, tx, tenant, c, read); err != nil {
			return err
		}
		if len(out.Answer) > 0 {
			err := authorizeTurnAnswer(ctx, tx, c, out.TurnID, out.Answer, read)
			if errors.Is(err, investigation.ErrResultExpired) {
				out.Answer = nil
				out.ErrorCode = "RESULT_EXPIRED"
				return nil
			}
			return err
		}
		return nil
	})
	return out, err
}
func (r Repository) Cancel(ctx context.Context, tenant, id uuid.UUID, subject, key string) error {
	turn, err := r.Turn(ctx, tenant, id, subject)
	if err != nil {
		return err
	}
	return r.Jobs.Stop(ctx, tenant, turn.JobID, subject, false, key)
}
func (r Repository) Archive(ctx context.Context, tenant, id uuid.UUID, subject string) error {
	return persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		c, err := loadConversation(ctx, tx, tenant, id, subject, true)
		if err != nil {
			return err
		}
		if _, err = authorizeConversation(ctx, tx, c); err != nil {
			return err
		}
		var active bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM investigation.assistant_turns WHERE tenant_id=$1 AND conversation_id=$2 AND state IN('queued','running'))`, tenant, id).Scan(&active); err != nil {
			return err
		}
		if active {
			return ErrBusy
		}
		_, err = tx.Exec(ctx, `UPDATE investigation.assistant_conversations SET archived_at=COALESCE(archived_at,clock_timestamp()),updated_at=clock_timestamp() WHERE tenant_id=$1 AND conversation_id=$2`, tenant, id)
		return err
	})
}

// List omits even titles of conversations whose current grants are invalid.
func (r Repository) List(ctx context.Context, tenant uuid.UUID, subject string, before time.Time, beforeID uuid.UUID, limit int) ([]Conversation, error) {
	page, err := r.ListPage(ctx, tenant, subject, before, beforeID, limit)
	return page.Items, err
}

type ConversationPage struct {
	Items    []Conversation
	Before   time.Time
	BeforeID uuid.UUID
	HasMore  bool
}

func (r Repository) ListPage(ctx context.Context, tenant uuid.UUID, subject string, before time.Time, beforeID uuid.UUID, limit int) (ConversationPage, error) {
	page := ConversationPage{Items: []Conversation{}}
	if limit < 1 || limit > 50 {
		return page, investigation.ErrInvalid
	}
	if before.IsZero() {
		before = time.Now().UTC().Add(time.Second)
		beforeID = uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")
	}
	err := persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		rs, err := newReadSubjects(ctx, tx, tenant, subject)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT conversation_id,updated_at FROM investigation.assistant_conversations WHERE tenant_id=$1 AND owner_subject = ANY($2) AND archived_at IS NULL AND (updated_at,conversation_id)<($3,$4) ORDER BY updated_at DESC,conversation_id DESC LIMIT 201`, tenant, rs.subjects(), before, beforeID)
		if err != nil {
			return err
		}
		type position struct {
			id uuid.UUID
			at time.Time
		}
		positions := []position{}
		for rows.Next() {
			var p position
			if err = rows.Scan(&p.id, &p.at); err != nil {
				rows.Close()
				return err
			}
			positions = append(positions, p)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for i, p := range positions {
			if i == 200 {
				page.HasMore = true
				break
			}
			page.Before = p.at
			page.BeforeID = p.id
			c, read, err := loadReadableConversation(ctx, tx, tenant, p.id, rs, false)
			if err != nil {
				return err
			}
			if _, err = authorizeRead(ctx, tx, c, read); err != nil {
				if errors.Is(err, investigation.ErrDenied) {
					continue
				}
				return err
			}
			if err = rs.recordAudit(ctx, tx, tenant, c, read); err != nil {
				return err
			}
			page.Items = append(page.Items, c)
			if len(page.Items) == limit {
				page.HasMore = i+1 < len(positions)
				break
			}
		}
		return nil
	})
	return page, err
}

type Event struct {
	EventID    int64           `json:"eventId"`
	TurnID     uuid.UUID       `json:"turnId"`
	Type       string          `json:"type"`
	Payload    json.RawMessage `json:"payload"`
	OccurredAt time.Time       `json:"occurredAt"`
}

func (r Repository) Events(ctx context.Context, tenant, id uuid.UUID, subject string, after int64) (Conversation, []Event, error) {
	var c Conversation
	out := []Event{}
	if after < 0 {
		return c, out, investigation.ErrInvalid
	}
	err := persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		var err error
		rs, err := newReadSubjects(ctx, tx, tenant, subject)
		if err != nil {
			return err
		}
		var read investigation.HistoricalRead
		c, read, err = loadReadableConversation(ctx, tx, tenant, id, rs, false)
		if err != nil {
			return err
		}
		if _, err = authorizeRead(ctx, tx, c, read); err != nil {
			return err
		}
		if err = rs.recordAudit(ctx, tx, tenant, c, read); err != nil {
			return err
		}
		var maxSeq int64
		if err = tx.QueryRow(ctx, `SELECT COALESCE(max(event_seq),0) FROM investigation.assistant_events WHERE tenant_id=$1 AND conversation_id=$2`, tenant, id).Scan(&maxSeq); err != nil {
			return err
		}
		if after > maxSeq {
			return errors.New("CURSOR_EXPIRED")
		}
		rows, err := tx.Query(ctx, `SELECT event_seq,COALESCE(turn_id,'00000000-0000-0000-0000-000000000000'::uuid),type,payload,occurred_at FROM investigation.assistant_events WHERE tenant_id=$1 AND conversation_id=$2 AND event_seq>$3 ORDER BY event_seq LIMIT 64`, tenant, id, after)
		if err != nil {
			return err
		}
		for rows.Next() {
			var e Event
			if err := rows.Scan(&e.EventID, &e.TurnID, &e.Type, &e.Payload, &e.OccurredAt); err != nil {
				rows.Close()
				return err
			}
			out = append(out, e)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for index, e := range out {
			if e.TurnID != uuid.Nil {
				if _, err := investigation.AssistantReadJobTxAs(ctx, tx, c.TenantID, read, c.ConversationID, e.TurnID, c.Binding.ExecutionScope); err != nil {
					if !errors.Is(err, investigation.ErrDenied) {
						return err
					}
					out[index].Type = "turn.out_of_scope"
					out[index].Payload = raw(map[string]string{"errorCode": "HISTORY_OUT_OF_SCOPE"})
					continue
				}
			}

			var v struct {
				Result json.RawMessage `json:"result"`
			}
			if json.Unmarshal(e.Payload, &v) != nil {
				return investigation.ErrInvalid
			}
			if len(v.Result) > 0 {
				if err := authorizeTurnAnswer(ctx, tx, c, e.TurnID, v.Result, read); err != nil {
					if !errors.Is(err, investigation.ErrResultExpired) {
						return err
					}
					out[index].Type = "answer.expired"
					out[index].Payload = raw(map[string]string{"errorCode": "RESULT_EXPIRED"})
				}
			}
		}
		return nil
	})
	return c, out, err
}
func (r Repository) QueryResult(ctx context.Context, tenant, id uuid.UUID, subject string) (investigation.StoredQueryResult, error) {
	var out investigation.StoredQueryResult
	err := persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		rs, err := newReadSubjects(ctx, tx, tenant, subject)
		if err != nil {
			return err
		}
		var cid, turnID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT t.conversation_id,t.turn_id FROM investigation.assistant_query_results q JOIN investigation.assistant_turns t USING(tenant_id,turn_id) WHERE q.tenant_id=$1 AND q.query_result_id=$2 AND q.owner_subject = ANY($3)`, tenant, id, rs.subjects()).Scan(&cid, &turnID); err != nil {
			return err
		}
		c, read, err := loadReadableConversation(ctx, tx, tenant, cid, rs, false)
		if err != nil {
			return err
		}
		if _, err = authorizeRead(ctx, tx, c, read); err != nil {
			return err
		}
		if err = rs.recordAudit(ctx, tx, tenant, c, read); err != nil {
			return err
		}
		j, err := investigation.AssistantReadJobTxAs(ctx, tx, tenant, read, cid, turnID, c.Binding.ExecutionScope)
		if err != nil {
			return err
		}
		out, err = investigation.ReadAssistantQueryResultTx(ctx, tx, j, id)
		return err
	})
	return out, err
}
