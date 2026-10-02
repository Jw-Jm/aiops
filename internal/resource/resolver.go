package resource

import (
	"context"
	"github.com/google/uuid"
	"time"
)

type ResolutionStatus string

const (
	Matched    ResolutionStatus = "matched"
	Conflicted ResolutionStatus = "conflicted"
	Unresolved ResolutionStatus = "unresolved"
)

type SourceRef struct {
	Domain, Tenant, Scope, APIGroup, Kind          string
	UID, UUID, ProviderID, Serial, Name, Namespace string
	SourceRegistrationID                           string
	ObservedAt                                     time.Time
	// Candidates are exact identity-index matches provided by the trusted source
	// repository, never deserialized from external query parameters.
	Candidates []CanonicalID
	ExplicitID string
}
type Resolution struct {
	Status       ResolutionStatus  `json:"status"`
	CanonicalID  CanonicalID       `json:"canonicalId"`
	Confidence   float64           `json:"confidence"`
	RuleVersion  string            `json:"ruleVersion"`
	SourceValues map[string]string `json:"sourceValues"`
	ObservedAt   time.Time         `json:"observedAt"`
	Aliases      []Alias           `json:"aliases"`
	Relations    []Relation        `json:"relations"`
}
type EntityResolver interface {
	Resolve(context.Context, SourceRef) (Resolution, error)
}
type Resolver struct{}

func NewResolver() *Resolver { return &Resolver{} }
func NormalizeHardwareUUID(raw string) string {
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return ""
	}
	return id.String()
}
func (r *Resolver) Resolve(ctx context.Context, ref SourceRef) (Resolution, error) {
	out := Resolution{Status: Unresolved, RuleVersion: "deterministic-identity/v1", ObservedAt: ref.ObservedAt, SourceValues: map[string]string{"uid": ref.UID, "uuid": ref.UUID, "providerId": ref.ProviderID, "serial": ref.Serial}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if ref.ObservedAt.IsZero() {
		return out, ErrIdentity
	}
	stable := ""
	switch ref.Domain {
	case "k8s":
		stable = ref.UID
	case "hardware":
		stable = NormalizeHardwareUUID(ref.UUID)
	}
	if stable == "" {
		return out, nil
	}
	id := CanonicalID{ref.Domain, ref.Tenant, ref.Scope, ref.APIGroup, ref.Kind, stable}
	if err := ValidateCanonicalID(id); err != nil {
		return out, err
	}
	if ref.ExplicitID != "" {
		explicit, err := ParseCanonicalID(ref.ExplicitID)
		if err != nil {
			return out, err
		}
		if explicit != id {
			out.Status = Conflicted
			return out, nil
		}
	}
	out.CanonicalID = id
	out.Confidence = 1
	out.Status = Matched
	provenance := Provenance{ref.SourceRegistrationID, out.RuleVersion, out.SourceValues, ref.ObservedAt}
	aliasKind := "uid"
	if ref.Domain == "hardware" {
		aliasKind = "system_uuid"
	}
	out.Aliases = []Alias{{ref.Tenant, ref.Scope, ref.SourceRegistrationID, aliasKind, stable, id.String(), provenance}}
	if ref.ProviderID != "" {
		out.Aliases = append(out.Aliases, Alias{ref.Tenant, ref.Scope, ref.SourceRegistrationID, "provider_id", ref.ProviderID, id.String(), provenance})
	}
	matches := map[string]CanonicalID{}
	for _, candidate := range ref.Candidates {
		if ValidateCanonicalID(candidate) != nil || candidate.Tenant != ref.Tenant {
			out.Status = Unresolved
			out.CanonicalID = CanonicalID{}
			out.Aliases = nil
			out.Confidence = 0
			out.Relations = nil
			return out, nil
		}
		if ref.Domain == "k8s" && ref.Kind == "Node" && candidate.Domain == "hardware" && candidate.Kind == "PhysicalServer" && candidate.StableID == NormalizeHardwareUUID(ref.UUID) {
			matches[candidate.String()] = candidate
			continue
		}
		if candidate.Domain == id.Domain && candidate.Scope == id.Scope && candidate.APIGroup == id.APIGroup && candidate.Kind == id.Kind && candidate != id {
			out.Status = Conflicted
			out.CanonicalID = CanonicalID{}
			out.Aliases = nil
			return out, nil
		}
	}
	if len(matches) > 1 {
		out.Status = Conflicted
		out.CanonicalID = CanonicalID{}
		out.Confidence = 0
		out.Aliases = nil
		return out, nil
	}
	for raw := range matches {
		out.Relations = []Relation{{From: raw, To: id.String(), Kind: "hosts", Provenance: provenance, Confidence: 1, ObservedAt: ref.ObservedAt, ValidFrom: ref.ObservedAt}}
	}
	// No fuzzy or name/serial scoring: these values are provenance only.
	return out, nil
}
