package assistant

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/audit"
	"ops-platform/internal/auth"
	"ops-platform/internal/authz"
	"ops-platform/internal/graph"
	"ops-platform/internal/identity"
	"ops-platform/internal/investigation"
)

// ownerRead is the owner-exact authority used by write paths and by callers
// that already loaded the conversation under the authenticated subject. It
// never authorises a mapped historical owner.
func ownerRead(c Conversation, subject string) investigation.HistoricalRead {
	return investigation.HistoricalRead{OwnerSubject: c.OwnerSubject, CurrentSubject: subject}
}

// readSubjects is the owner allowlist of a single read transaction. It always
// contains the authenticated subject and adds pre-native subjects only when a
// reviewed inventory produced an explicit mapping for the *current native
// account*. Every candidate owner is re-authorized against current authority;
// the mapping never becomes a role and never rewrites a stored owner.
type readSubjects struct {
	actor    string
	mappings map[string]identity.LegacySubjectMapping
	audited  bool
}

func newReadSubjects(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, actor string) (readSubjects, error) {
	out := readSubjects{actor: actor, mappings: map[string]identity.LegacySubjectMapping{}}
	if actor == "" {
		return out, investigation.ErrDenied
	}
	native, ok := auth.NativeSessionFromContext(ctx)
	if !ok {
		// Legacy requests have no account to map onto; they only see their own
		// historical subject, exactly as before.
		return out, nil
	}
	if native.Session.TenantID != tenant || native.Session.Subject != actor {
		return out, investigation.ErrDenied
	}
	mappings, err := identity.MappedLegacySubjectsTx(ctx, tx, tenant, native.Session.UserID)
	if err != nil {
		// A missing mapping function or reviewed inventory is a denial, not a
		// reason to fall back to name/subject matching.
		return out, nil
	}
	for _, mapping := range mappings {
		if mapping.LegacySubject == "" || mapping.LegacySubject == actor {
			continue
		}
		out.mappings[mapping.LegacySubject] = mapping
	}
	return out, nil
}

// subjects returns the SQL allowlist for the owner column. It is always a
// closed set: the authenticated subject plus explicitly reviewed mappings.
func (r readSubjects) subjects() []string {
	out := []string{r.actor}
	for subject := range r.mappings {
		out = append(out, subject)
	}
	return out
}

// authority resolves one stored owner into a read authority. An owner other
// than the actor is only readable through an explicit reviewed mapping.
func (r readSubjects) authority(owner string) (investigation.HistoricalRead, error) {
	if owner == "" {
		return investigation.HistoricalRead{}, investigation.ErrDenied
	}
	if owner == r.actor {
		return investigation.HistoricalRead{OwnerSubject: owner, CurrentSubject: r.actor}, nil
	}
	mapping, ok := r.mappings[owner]
	if !ok {
		return investigation.HistoricalRead{}, investigation.ErrDenied
	}
	return investigation.HistoricalRead{
		OwnerSubject:   owner,
		CurrentSubject: r.actor,
		Mapped:         true,
		MappingID:      mapping.MappingID,
		EvidenceDigest: mapping.EvidenceDigest,
	}, nil
}

// recordAudit keeps one access record per read transaction, bound to the frozen
// owner and the reviewed mapping that allowed the read. It never stores the
// conversation content or any credential.
func (r *readSubjects) recordAudit(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, c Conversation, read investigation.HistoricalRead) error {
	if !read.Mapped || r.audited {
		return nil
	}
	if _, err := audit.Append(ctx, tx, audit.Entry{
		TenantID:   tenant,
		RecordID:   uuid.Must(uuid.NewV7()),
		EventType:  "assistant.history.mapped_owner.read",
		EntityKind: "assistant_conversation",
		Subject:    read.CurrentSubject,
		Payload: map[string]any{
			"conversationId":  c.ConversationID.String(),
			"ownerSubject":    read.OwnerSubject,
			"mappingId":       read.MappingID.String(),
			"mappingEvidence": read.EvidenceDigest,
			"authority":       "current-native",
		},
	}); err != nil {
		return err
	}
	r.audited = true
	return nil
}

// loadReadableConversation loads a conversation whose owner is either the
// authenticated subject or an explicitly mapped historical subject.
func loadReadableConversation(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, rs readSubjects, lock bool) (Conversation, investigation.HistoricalRead, error) {
	var c Conversation
	var read investigation.HistoricalRead
	var binding []byte
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	err := tx.QueryRow(ctx, `SELECT `+conversationColumns+` FROM investigation.assistant_conversations WHERE tenant_id=$1 AND conversation_id=$2 AND owner_subject = ANY($3)`+suffix, tenant, id, rs.subjects()).Scan(&c.TenantID, &c.ConversationID, &c.OwnerSubject, &c.Title, &c.Mode, &c.AuthorizationDomain, &c.ClusterUID, &binding, &c.Revision, &c.ArchivedAt, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return c, read, err
	}
	if json.Unmarshal(binding, &c.Binding) != nil {
		return c, read, investigation.ErrInvalid
	}
	read, err = rs.authority(c.OwnerSubject)
	return c, read, err
}

// loadReadableTurn resolves a turn, its conversation and the read authority, and
// re-checks the frozen binding. It is the shared entry for turn-scoped reads.
func loadReadableTurn(ctx context.Context, tx pgx.Tx, tenant, turn uuid.UUID, rs *readSubjects) (Conversation, Turn, investigation.HistoricalRead, error) {
	t, err := loadTurn(ctx, tx, tenant, turn)
	if err != nil {
		return Conversation{}, Turn{}, investigation.HistoricalRead{}, err
	}
	c, read, err := loadReadableConversation(ctx, tx, tenant, t.ConversationID, *rs, false)
	if err != nil {
		return c, t, read, err
	}
	if _, err = authorizeRead(ctx, tx, c, read); err != nil {
		return c, t, read, err
	}
	if err = rs.recordAudit(ctx, tx, tenant, c, read); err != nil {
		return c, t, read, err
	}
	return c, t, read, nil
}

// authorizeRead keeps the frozen owner's evidence intact while proving that the
// reading account still holds current authority for it. The frozen grant
// snapshot is never compared with, or replaced by, a native grant; only the
// cluster/namespace/resource containment is required in addition to the frozen
// snapshot's own integrity.
func authorizeRead(ctx context.Context, tx pgx.Tx, c Conversation, read investigation.HistoricalRead) (graph.Scope, error) {
	if !read.Mapped {
		return authorizeConversation(ctx, tx, c)
	}
	native, ok := auth.NativeSessionFromContext(ctx)
	if !ok || native.Session.TenantID != c.TenantID || native.Session.Subject != read.CurrentSubject {
		return graph.Scope{}, investigation.ErrDenied
	}
	if !investigation.ScopeSubset(c.Binding.GrantSnapshot, c.Binding.ExecutionScope) {
		return graph.Scope{}, investigation.ErrDenied
	}
	var grant graph.Scope
	switch c.AuthorizationDomain {
	case "audit":
		allowed, err := authz.NativeRoleAuthorized(ctx, tx, auth.RequestContext{TenantID: c.TenantID, Subject: read.CurrentSubject}, "platform_admin")
		if err != nil || !allowed {
			return graph.Scope{}, investigation.ErrDenied
		}
		var revision int64
		if err := tx.QueryRow(ctx, `SELECT revision FROM platform.tenants WHERE tenant_id=$1 AND status='active'`, c.TenantID).Scan(&revision); err != nil {
			return graph.Scope{}, investigation.ErrDenied
		}
		grant = graph.Scope{Tenant: c.TenantID.String(), Namespaces: []string{}, Resources: []string{}}
	case "operational":
		if c.ClusterUID == "" {
			return graph.Scope{}, investigation.ErrDenied
		}
		effective, err := graph.EffectiveTx(ctx, tx, c.TenantID.String(), read.CurrentSubject, c.ClusterUID, true)
		if err != nil || !investigation.ScopeCoversMappedOwner(effective, c.Binding.ExecutionScope) {
			return graph.Scope{}, investigation.ErrDenied
		}
		grant = effective
	default:
		return graph.Scope{}, investigation.ErrDenied
	}
	if err := authorizeFrozenBinding(ctx, tx, c); err != nil {
		return graph.Scope{}, err
	}
	return grant, nil
}
