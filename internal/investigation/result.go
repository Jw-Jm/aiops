package investigation

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/contract"
	"ops-platform/internal/evidence"
	"ops-platform/internal/finding"
	"ops-platform/internal/incident"
	"ops-platform/internal/rca"
	"slices"
	"time"
)

type CandidateSuggestion struct {
	CandidateKey string `json:"candidateKey"`
	Reason       string `json:"reason"`
}
type Proposal struct {
	SchemaVersion    string                `json:"schemaVersion"`
	Status           string                `json:"status"`
	Summary          string                `json:"summary"`
	EvidenceRefs     []string              `json:"evidenceRefs"`
	CandidateUpdates []CandidateSuggestion `json:"candidateUpdates"`
	ActionPlans      []json.RawMessage     `json:"actionPlans"`
	Partial          bool                  `json:"partial"`
	DegradedSources  []string              `json:"degradedSources"`
}

// Complete validates a proposal against committed reads and current platform
// authority. Model proposals never mutate deterministic RCA or create execution
// sessions. Every final reference retains the existing dependency/hold closure.
func (r Repository) Complete(ctx context.Context, l Lease, b []byte) error {
	var p Proposal
	if contract.Validate("https://ops.local/schemas/investigation-result/v1", b) != nil {
		return ErrInvalid
	}
	if len(b) > 64<<10 || strictJSON(b, &p) != nil || p.SchemaVersion != "investigation-result/v1" || (p.Status != "unresolved" && p.Status != "probable") || len(p.Summary) > 8192 || len(p.EvidenceRefs) > 100 || len(p.CandidateUpdates) > 50 || len(p.ActionPlans) > 10 || p.Summary == "" {
		return ErrInvalid
	}
	// Graph/source runtime checks execute before entering the commit transaction.
	// Their frozen revision is then revalidated against Incident/Recipe/facts.
	var runtimeRevision int64
	if len(p.CandidateUpdates) > 0 || len(p.ActionPlans) > 0 {
		if r.CurrentRCA == nil || r.Trust == nil {
			return ErrDenied
		}
		job, err := r.Get(ctx, l.TenantID, l.JobID)
		if err != nil {
			return err
		}
		runtimeRevision, err = r.CurrentRCA(ctx, job)
		if err != nil {
			return ErrDenied
		}
	} else if p.Status == "probable" {
		p.Status = "unresolved"
		p.Partial = true
	}
	return r.withLease(ctx, l, func(tx pgx.Tx, j Job) error {
		if j.Reserved != (Usage{}) {
			return ErrLease
		}
		available := map[string]bool{}
		rows, err := tx.Query(ctx, `SELECT result FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND state='succeeded' AND tool_name<>'model'`, j.TenantID, j.JobID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var b []byte
			var v struct {
				EvidenceRefs []string `json:"evidenceRefs"`
			}
			if rows.Scan(&b) != nil || json.Unmarshal(b, &v) != nil {
				rows.Close()
				return ErrInvalid
			}
			for _, ref := range v.EvidenceRefs {
				available[ref] = true
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, ref := range p.EvidenceRefs {
			id, err := uuid.Parse(ref)
			if err != nil || !available[ref] || seen[ref] {
				return ErrDenied
			}
			seen[ref] = true
			ev, err := evidence.GetTx(ctx, tx, j.TenantID, id, j.Scope)
			if err != nil || ev.TenantID != j.TenantID.String() || !slices.Contains(j.Budget.AllowedDataClasses, ev.DataClass) || ev.SchemaVersion != "evidence/v2" {
				return ErrDenied
			}
			// Archived Incident evidence is historical by design. Its age alone
			// cannot invalidate it; source retention/replay and the current RCA
			// Recipe gate define validity. Unreliable event time stays unresolved.
			if !ev.TimeReliable {
				p.Partial = true
				p.Status = "unresolved"
				p.DegradedSources = append(p.DegradedSources, "expired-evidence")
			}
			if time.Now().After(ev.SourceRetentionUntil) && ev.ReplayState != "archived_verified" {
				return ErrDenied
			}
			if err = evidence.Protect(ctx, tx, j.TenantID, id, j.JobID, "investigation", time.Now().Add(365*24*time.Hour), true); err != nil {
				return err
			}
		}
		if len(p.EvidenceRefs) == 0 {
			p.Partial = true
			p.Status = "unresolved"
			p.DegradedSources = append(p.DegradedSources, "no-evidence")
		}
		var revision int64
		var rcaRaw []byte
		err = tx.QueryRow(ctx, `SELECT revision,result FROM incident.rca_revisions WHERE tenant_id=$1 AND incident_id=$2 AND NOT superseded ORDER BY revision DESC LIMIT 1`, j.TenantID, j.IncidentID).Scan(&revision, &rcaRaw)
		var frozen rca.Revision
		decodeErr := json.Unmarshal(rcaRaw, &frozen)
		deterministic := frozen.Result
		if err != nil || decodeErr != nil {
			if len(p.CandidateUpdates) > 0 || len(p.ActionPlans) > 0 {
				return ErrDenied
			}
			p.Status = "unresolved"
		} else {
			for _, c := range p.CandidateUpdates {
				valid := false
				for _, known := range deterministic.Candidates {
					if c.CandidateKey == known.Key && len(c.Reason) > 0 && len(c.Reason) <= 4096 {
						valid = true
						for _, ref := range known.EvidenceRefs {
							if !seen[ref] {
								valid = false
							}
						}
					}
				}
				if !valid {
					return ErrDenied
				}
			}
		}
		if len(p.CandidateUpdates) > 0 || len(p.ActionPlans) > 0 {
			if runtimeRevision != revision || p.Partial {
				return ErrDenied
			}
			i, err := incident.Load(ctx, tx, j.TenantID, j.IncidentID.String(), true)
			if err != nil {
				return err
			}
			var vid uuid.UUID
			var manifestRaw, recipeRaw []byte
			var base int64
			if tx.QueryRow(ctx, `SELECT recipe_version_id,input_manifest,base_incident_revision FROM incident.rca_revisions WHERE tenant_id=$1 AND incident_id=$2 AND revision=$3 AND NOT superseded`, j.TenantID, j.IncidentID, revision).Scan(&vid, &manifestRaw, &base) != nil || base != i.Revision {
				return ErrDenied
			}
			if tx.QueryRow(ctx, `SELECT content FROM platform.registry_versions WHERE tenant_id=$1 AND version_id=$2`, j.TenantID, vid).Scan(&recipeRaw) != nil {
				return ErrDenied
			}
			recipe, err := rca.DecodeRecipe(recipeRaw)
			if err != nil {
				return ErrDenied
			}
			if rca.VerifyRecipeForRead(ctx, tx, j.TenantID, &vid, i.ClusterUID, i.Namespace, recipe, r.Trust) != nil {
				return ErrDenied
			}
			var manifest rca.Input
			if json.Unmarshal(manifestRaw, &manifest) != nil {
				return ErrDenied
			}
			facts, err := rca.FreezeFindingsTx(ctx, tx, j.TenantID, j.IncidentID.String())
			if err != nil || finding.Hash(facts) != finding.Hash(manifest.FindingRevisions) {
				return ErrDenied
			}
			for _, ref := range manifest.Evidence {
				if !seen[ref.EvidenceID] {
					return ErrDenied
				}
			}
		}
		for _, plan := range p.ActionPlans {
			if contract.Validate("https://ops.local/schemas/action-plan/v2", plan) != nil {
				return ErrInvalid
			}
			var a struct {
				Tenant   string `json:"tenantId"`
				Incident string `json:"incidentId"`
				Revision int64  `json:"rcaRevision"`
				Target   string `json:"targetCanonicalId"`
				State    string `json:"state"`
				DataRisk string `json:"dataRisk"`
			}
			if json.Unmarshal(plan, &a) != nil || a.Tenant != j.TenantID.String() || a.Incident != j.IncidentID.String() || a.Revision != revision || a.State != "suggested" || !slices.Contains(j.Budget.AllowedDataClasses, a.DataRisk) {
				return ErrDenied
			}
			var ns string
			// Historical identities remain readable for replay, but cannot be
			// current recommendation targets. The API retains read-only access
			// to resource inventory; this check grants no execution authority.
			if tx.QueryRow(ctx, `SELECT namespace FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2 AND deleted_at IS NULL`, j.TenantID, a.Target).Scan(&ns) != nil || !j.Scope.Allows(a.Target, ns) {
				return ErrDenied
			}
		}
		state := "succeeded"
		if p.Partial {
			state = "partial"
		}
		dedup := []string{}
		for _, source := range p.DegradedSources {
			if !slices.Contains(dedup, source) && len(dedup) < 100 {
				dedup = append(dedup, source)
			}
		}
		p.DegradedSources = dedup
		if contract.Validate("https://ops.local/schemas/investigation-result/v1", raw(p)) != nil {
			return ErrInvalid
		}
		if _, err = tx.Exec(ctx, `UPDATE investigation.jobs SET state=$3,result=$4 WHERE tenant_id=$1 AND job_id=$2`, j.TenantID, j.JobID, state, raw(p)); err != nil {
			return err
		}
		return event(ctx, tx, j, "result", map[string]any{"state": state, "result": p})
	})
}
