package finding

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"ops-platform/internal/contract"
	"ops-platform/internal/resource"
	"strings"
	"time"
)

var (
	ErrInvalid      = errors.New("INVALID_ARGUMENT")
	ErrConflict     = errors.New("IDEMPOTENCY_CONFLICT")
	ErrUnauthorized = errors.New("SOURCE_SCOPE_UNVERIFIED")
)

// Envelope v2 separates event time from the database reception clock. Identity
// claims never grant a scope; only the authenticated BoundSourceContext does.
type Envelope struct {
	SchemaVersion        string          `json:"schemaVersion"`
	EventID              string          `json:"eventId"`
	IdempotencyKey       string          `json:"idempotencyKey"`
	TenantID             string          `json:"tenantId,omitempty"`
	SourceRegistrationID string          `json:"sourceRegistrationId,omitempty"`
	ClusterUID           string          `json:"clusterUid,omitempty"`
	ResourceCanonicalID  string          `json:"resourceCanonicalId"`
	Namespace            string          `json:"namespace"`
	RuleID               string          `json:"ruleId"`
	RuleFamily           string          `json:"ruleFamily"`
	NormalizedSymptom    string          `json:"normalizedSymptom"`
	OccurrenceID         string          `json:"occurrenceId"`
	StartsAt             time.Time       `json:"startsAt"`
	ObservedAt           time.Time       `json:"observedAt"`
	SourceSequence       int64           `json:"sourceSequence"`
	TimeReliable         bool            `json:"timeReliable"`
	State                string          `json:"lifecycleState"`
	Severity             string          `json:"severity"`
	Payload              json.RawMessage `json:"payload"`
	PayloadDigest        string          `json:"payloadDigest,omitempty"`
	EvidenceRefs         []string        `json:"evidenceRefs"`
}
type Finding struct {
	Envelope
	FindingID         string    `json:"findingId"`
	SourceFingerprint string    `json:"sourceFingerprint"`
	AggregateRevision int64     `json:"aggregateRevision"`
	ReceivedAt        time.Time `json:"receivedAt"`
	FirstReceivedAt   time.Time `json:"firstReceivedAt"`
	SourceRevision    int64     `json:"sourceRevision"`
	Digest            string    `json:"digest"`
}
type Disposition string

const (
	Accepted  Disposition = "accepted"
	Duplicate Disposition = "duplicate"
	Stale     Disposition = "stale"
)

func Hash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}
func Fingerprint(tenant, source, cluster string, e Envelope) string {
	return Hash([]string{"source-fingerprint/v1", tenant, source, cluster, e.RuleID, e.ResourceCanonicalID, e.NormalizedSymptom})
}
func SemanticDigest(e Envelope) string {
	e.EventID = ""
	e.IdempotencyKey = ""
	e.PayloadDigest = ""
	e.StartsAt = e.StartsAt.UTC()
	e.ObservedAt = e.ObservedAt.UTC()
	e.TenantID = ""
	e.SourceRegistrationID = ""
	e.ClusterUID = ""
	// Sort object keys and discard whitespace without rounding publisher numeric
	// facts through float64. Exact JSON number spellings remain digest-significant.
	var v any
	decoder := json.NewDecoder(bytes.NewReader(e.Payload))
	decoder.UseNumber()
	_ = decoder.Decode(&v)
	e.Payload, _ = json.Marshal(v)
	return Hash(e)
}
func (e Envelope) Validate(tenant, source, cluster string) error {
	id, err := resource.ParseCanonicalID(e.ResourceCanonicalID)
	if err != nil || id.Tenant != tenant || id.Scope != cluster {
		return ErrUnauthorized
	}
	if id.Domain == "k8s" && e.Namespace == "" {
		clusterKinds := map[string]bool{"Node": true, "Namespace": true, "PersistentVolume": true, "StorageClass": true, "CSIDriver": true, "CSINode": true, "VolumeAttachment": true}
		if !clusterKinds[id.Kind] {
			return ErrUnauthorized
		}
	}
	if (e.TenantID != "" && e.TenantID != tenant) || (e.SourceRegistrationID != "" && e.SourceRegistrationID != source) || (e.ClusterUID != "" && e.ClusterUID != cluster) {
		return ErrUnauthorized
	}
	if e.SchemaVersion != "finding-envelope/v2" || e.SourceSequence < 0 || e.ObservedAt.IsZero() || e.StartsAt.IsZero() || e.ObservedAt.Before(e.StartsAt) || (e.State != "firing" && e.State != "resolved") {
		return ErrInvalid
	}
	for _, v := range []string{e.EventID, e.IdempotencyKey, e.RuleID, e.RuleFamily, e.NormalizedSymptom, e.OccurrenceID} {
		if v == "" || len(v) > 512 || strings.TrimSpace(v) != v || strings.ContainsAny(v, "\x00\r\n") {
			return ErrInvalid
		}
	}
	if e.Severity != "critical" && e.Severity != "warning" && e.Severity != "info" && e.Severity != "unknown" {
		return ErrInvalid
	}
	var p map[string]any
	if len(e.Payload) > 32<<10 || json.Unmarshal(e.Payload, &p) != nil || p == nil || len(e.EvidenceRefs) > 100 {
		return ErrInvalid
	}
	for _, id := range e.EvidenceRefs {
		if _, err := uuid.Parse(id); err != nil {
			return ErrInvalid
		}
	}
	if e.PayloadDigest != "" && e.PayloadDigest != SemanticDigest(e) {
		return ErrConflict
	}
	raw, _ := json.Marshal(e)
	if contract.Validate("https://ops.local/schemas/finding-envelope/v2", raw) != nil {
		return ErrInvalid
	}
	return nil
}

// Resolved is terminal for an occurrence. A new startsAt needs a new occurrence.
// No receive-time bucket participates in ordering or deduplication.
func Reduce(old *Finding, e Envelope) (string, bool) {
	if old == nil {
		return e.State, true
	}
	if old.State == "resolved" {
		return old.State, false
	}
	if old.SourceSequence > 0 && e.SourceSequence > 0 {
		if e.SourceSequence < old.SourceSequence {
			return old.State, false
		}
		if e.SourceSequence == old.SourceSequence {
			return old.State, false
		}
	} else {
		if e.ObservedAt.Before(old.ObservedAt) {
			return old.State, false
		}
		if e.ObservedAt.Equal(old.ObservedAt) && e.State != "resolved" {
			return old.State, false
		}
	}
	return e.State, true
}
