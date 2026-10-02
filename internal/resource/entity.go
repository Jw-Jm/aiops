package resource

import (
	"encoding/json"
	"time"
)

type Entity struct {
	SourceRefs      []string          `json:"sourceRefs"`
	FirstObservedAt time.Time         `json:"firstObservedAt"`
	CanonicalID     string            `json:"canonicalId"`
	Kind            string            `json:"kind"`
	Namespace       string            `json:"namespace"`
	Name            string            `json:"name"`
	Labels          map[string]string `json:"labels"`
	Attributes      map[string]any    `json:"attributes"`
	UpdatedAt       time.Time         `json:"updatedAt"`
}
type Provenance struct {
	SourceRegistrationID string            `json:"sourceRegistrationId"`
	RuleVersion          string            `json:"ruleVersion"`
	SourceValues         map[string]string `json:"sourceValues"`
	ObservedAt           time.Time         `json:"observedAt"`
}
type Relation struct {
	From       string     `json:"from"`
	To         string     `json:"to"`
	Kind       string     `json:"kind"`
	Provenance Provenance `json:"provenance"`
	Confidence float64    `json:"confidence"`
	ObservedAt time.Time  `json:"observedAt"`
	ValidFrom  time.Time  `json:"validFrom"`
	ValidTo    *time.Time `json:"validTo,omitempty"`
	TTLSeconds int        `json:"ttl"`
}

// MarshalJSON supplies the frozen Entity identity fields from its validated
// canonical identifier. Query time never replaces source observation time.
func (e Entity) MarshalJSON() ([]byte, error) {
	id, err := ParseCanonicalID(e.CanonicalID)
	if err != nil {
		return nil, err
	}
	type base Entity
	first := e.FirstObservedAt
	if first.IsZero() {
		first = e.UpdatedAt
	}
	refs := e.SourceRefs
	if refs == nil {
		refs = []string{}
	}
	labels := e.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	attributes := e.Attributes
	if attributes == nil {
		attributes = map[string]any{}
	}
	var namespace *string
	if e.Namespace != "" {
		namespace = &e.Namespace
	}
	return json.Marshal(struct {
		base
		TenantID        string            `json:"tenantId"`
		Domain          string            `json:"domain"`
		Scope           string            `json:"scope"`
		APIGroup        string            `json:"apiGroup"`
		StableID        string            `json:"stableId"`
		Namespace       *string           `json:"namespace"`
		Labels          map[string]string `json:"labels"`
		Attributes      map[string]any    `json:"attributes"`
		SourceRefs      []string          `json:"sourceRefs"`
		FirstObservedAt time.Time         `json:"firstObservedAt"`
		LastObservedAt  time.Time         `json:"lastObservedAt"`
	}{base(e), id.Tenant, id.Domain, id.Scope, id.APIGroup, id.StableID, namespace, labels, attributes, refs, first, e.UpdatedAt})
}
