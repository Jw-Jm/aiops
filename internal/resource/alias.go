package resource

// Alias is scoped to the immutable tenant and source scope. Values retain case
// and leading zeroes unless the rule explicitly normalizes UUID syntax.
type Alias struct {
	Tenant               string     `json:"tenant"`
	Scope                string     `json:"scope"`
	SourceRegistrationID string     `json:"sourceRegistrationId"`
	Kind                 string     `json:"kind"`
	Value                string     `json:"value"`
	CanonicalID          string     `json:"canonicalId"`
	Provenance           Provenance `json:"provenance"`
}
