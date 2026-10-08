package app

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/evidence"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	"ops-platform/internal/source"
	"strconv"
	"time"
)

func SubmitSP05Candidate(ctx context.Context, archive *evidence.ArchiveService, binding evidence.Binding, c finding.FindingCandidate) error {
	bound := source.BoundSourceContext{TenantID: uuid.MustParse(binding.Tenant), SourceID: uuid.MustParse(binding.SourceID), RegistrationRevision: binding.Revision}
	if err := persistenceSourceContext(ctx, archive.Pool, &bound); err != nil {
		return err
	}
	if c.ObservedAt.IsZero() || c.NativeIdentity == "" || c.IndependenceGroup == "" || len(c.Data) == 0 {
		return finding.ErrInvalid
	}
	if err := attachPodMetricContext(ctx, archive, binding, &c); err != nil {
		return err
	}
	service := finding.Service{Pool: archive.Pool}
	key := finding.Hash([]string{c.ResourceCanonicalID, c.RuleID})
	occurrence, start, err := service.PollIdentity(ctx, bound, key, c.State, c.Namespace, c.ObservedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	// A re-list of an unchanged native resource is a new state observation,
	// while retries inside this bounded observation window share one identity.
	// This avoids expiring a continuously verified condition after five minutes.
	window := c.ObservedAt.UTC().Truncate(30 * time.Second).Format(time.RFC3339Nano)
	identity := finding.Hash([]string{binding.SourceID, key, c.NativeIdentity, window})
	evidenceID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(identity)).String()
	evidenceType := "resource_state"
	// Type is derived from the fixed admitted native template, never a request
	// field. High resource utilization remains context, not a causal predicate.
	if c.QueryTemplateVersion == "sp05-official-metrics/v1" || (binding.SourceType == "redfish" && c.QueryTemplateVersion == "sp05-hardware/v1" && c.RuleID == "redfish/dimm-uncorrectable-ecc/v1") {
		evidenceType = "metric"
	}
	metadata := evidence.Evidence{SourceScopeDigest: evidence.BindingScopeDigest(binding), SchemaVersion: "evidence/v2", EvidenceID: evidenceID, TenantID: binding.Tenant, ResourceCanonicalID: c.ResourceCanonicalID, Type: evidenceType, DataClass: "D0", SourceRegistrationID: binding.SourceID, SourceRevision: binding.Revision, SourceSystem: binding.SourceType, BackendLogicalID: binding.BackendLogicalID, QueryTemplateVersion: c.QueryTemplateVersion, QueryHash: identity, EffectiveScope: graph.Scope{Tenant: binding.Tenant, Cluster: bound.ClusterUID, Namespaces: []string{c.Namespace}, ClusterScoped: c.Namespace == "", AuthorizationRevision: "collector/" + binding.SourceID + "/" + strconv.FormatInt(binding.Revision, 10)}, EvaluatedAt: c.ObservedAt, ObservedFrom: c.ObservedAt, ObservedTo: c.ObservedAt, SourceRetentionUntil: c.ObservedAt, TimeReliable: c.TimeReliable, ReplayState: "archive_pending", ContentDigest: evidence.Digest(c.Data), IndependenceGroup: binding.SourceID + "/" + c.IndependenceGroup, DerivationEvidenceRefs: []string{}, Data: c.Data}
	metadata.DerivationEvidenceRefs = append(metadata.DerivationEvidenceRefs, c.EvidenceRefs...)
	if c.Namespace == "" {
		metadata.EffectiveScope.Namespaces = nil
	}
	previous, readErr := (evidence.Repository{Pool: archive.Pool}).Get(ctx, bound.TenantID, uuid.MustParse(evidenceID), metadata.EffectiveScope)
	if readErr == nil {
		if previous.ContentDigest != metadata.ContentDigest || previous.QueryHash != metadata.QueryHash {
			return finding.ErrConflict
		}
		metadata.EvaluatedAt, metadata.ObservedFrom, metadata.ObservedTo, metadata.SourceRetentionUntil = previous.EvaluatedAt, previous.ObservedFrom, previous.ObservedTo, previous.SourceRetentionUntil
	} else if !errors.Is(readErr, pgx.ErrNoRows) {
		return readErr
	}
	if err := archive.Prepare(ctx, metadata, c.Namespace, c.ObservedAt.Add(181*24*time.Hour)); err != nil {
		return err
	}
	envelope := finding.Envelope{SchemaVersion: "finding-envelope/v2", EventID: identity, IdempotencyKey: evidenceID, ResourceCanonicalID: c.ResourceCanonicalID, Namespace: c.Namespace, RuleID: c.RuleID, RuleFamily: c.RuleFamily, NormalizedSymptom: c.NormalizedSymptom, OccurrenceID: occurrence, StartsAt: start, ObservedAt: metadata.ObservedTo, TimeReliable: c.TimeReliable, State: c.State, Severity: "warning", Payload: c.Data, EvidenceRefs: []string{evidenceID}}
	envelope.EvidenceRefs = append(envelope.EvidenceRefs, c.EvidenceRefs...)
	if _, _, err := service.Ingest(ctx, bound, envelope); err != nil {
		return err
	}
	if c.State == "resolved" {
		return service.ClosePoll(ctx, bound, key, occurrence)
	}
	return nil
}
