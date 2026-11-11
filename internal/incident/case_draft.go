package incident

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/audit"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	"ops-platform/internal/persistence"
)

type CaseDraft struct {
	DocumentID       uuid.UUID `json:"documentId"`
	Kind             string    `json:"kind"`
	Version          int       `json:"version"`
	State            string    `json:"state"`
	IncidentID       string    `json:"incidentId"`
	IncidentRevision int64     `json:"incidentRevision"`
	IncidentState    string    `json:"incidentState"`
	Title            string    `json:"title"`
	Summary          string    `json:"summary"`
	Content          string    `json:"content"`
	CreatorSubject   string    `json:"creatorSubject"`
	CreatedAt        time.Time `json:"createdAt"`
}

type CaseDraftInput struct {
	Kind             string `json:"kind"`
	IncidentID       string `json:"incidentId"`
	ExpectedRevision int64  `json:"expectedRevision"`
	Title            string `json:"title"`
	Summary          string `json:"summary"`
	Content          string `json:"content"`
}

func (in CaseDraftInput) Validate() error {
	if in.Kind != "case" || in.ExpectedRevision < 1 {
		return finding.ErrInvalid
	}
	if _, err := uuid.Parse(in.IncidentID); err != nil {
		return finding.ErrInvalid
	}
	for _, field := range []struct {
		value string
		limit int
	}{{in.Title, 160}, {in.Summary, 500}, {in.Content, 16000}} {
		if !utf8.ValidString(field.value) || strings.TrimSpace(field.value) == "" || strings.ContainsRune(field.value, 0) || utf8.RuneCountInString(field.value) > field.limit {
			return finding.ErrInvalid
		}
	}
	return nil
}

func (s Service) CreateCaseDraft(ctx context.Context, scope graph.Scope, subject string, input CaseDraftInput) (CaseDraft, error) {
	var out CaseDraft
	if err := input.Validate(); err != nil {
		return out, err
	}
	tenant, err := uuid.Parse(scope.Tenant)
	if err != nil {
		return out, graph.ErrScope
	}
	err = persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		i, _, err := s.loadAuthorized(ctx, tx, tenant, scope, subject, input.IncidentID)
		if err != nil {
			return err
		}
		if i.Revision != input.ExpectedRevision {
			return ErrRevision
		}
		out = CaseDraft{DocumentID: uuid.Must(uuid.NewV7()), Kind: "case", Version: 1, State: "draft", IncidentID: i.IncidentID, IncidentRevision: i.Revision, IncidentState: i.State, Title: strings.TrimSpace(input.Title), Summary: strings.TrimSpace(input.Summary), Content: strings.TrimSpace(input.Content), CreatorSubject: subject}
		err = tx.QueryRow(ctx, `INSERT INTO incident.case_drafts(tenant_id,document_id,incident_id,incident_revision,incident_state,title,summary,content,creator_subject) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING created_at`, tenant, out.DocumentID, out.IncidentID, out.IncidentRevision, out.IncidentState, out.Title, out.Summary, out.Content, subject).Scan(&out.CreatedAt)
		if err != nil {
			return err
		}
		if _, err = appendTimeline(ctx, tx, i, "case_draft", subject, "case_draft_created", map[string]any{"documentId": out.DocumentID, "version": 1}); err != nil {
			return err
		}
		_, err = audit.Append(ctx, tx, audit.Entry{TenantID: tenant, RecordID: uuid.Must(uuid.NewV7()), EntityID: out.DocumentID, EntityKind: "knowledge_document", EventType: "knowledge.case_draft.create", Subject: subject, Payload: map[string]any{"incidentId": i.IncidentID, "incidentRevision": i.Revision, "version": 1}})
		return err
	})
	return out, err
}

// Reads reauthorize the entire origin incident, including its linked resources.
func (s Service) ReadCaseDrafts(ctx context.Context, scope graph.Scope, subject, incidentID string, documentID uuid.UUID, after uuid.UUID) ([]CaseDraft, error) {
	out := []CaseDraft{}
	tenant, err := uuid.Parse(scope.Tenant)
	if err != nil {
		return nil, graph.ErrScope
	}
	err = persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		i, err := Load(ctx, tx, tenant, incidentID, false)
		if err != nil {
			return err
		}
		if err = AuthorizeAll(ctx, tx, scope, subject, i); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT document_id,incident_revision,incident_state,title,summary,content,creator_subject,created_at FROM incident.case_drafts WHERE tenant_id=$1 AND incident_id=$2 AND ($3::uuid IS NULL OR document_id=$3) AND ($4::uuid IS NULL OR document_id>$4) ORDER BY document_id LIMIT 51`, tenant, incidentID, caseDraftID(documentID), caseDraftID(after))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			d := CaseDraft{Kind: "case", Version: 1, State: "draft", IncidentID: incidentID}
			if err = rows.Scan(&d.DocumentID, &d.IncidentRevision, &d.IncidentState, &d.Title, &d.Summary, &d.Content, &d.CreatorSubject, &d.CreatedAt); err != nil {
				return err
			}
			out = append(out, d)
		}
		return rows.Err()
	})
	return out, err
}

func caseDraftID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}
