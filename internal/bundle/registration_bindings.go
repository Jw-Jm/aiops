package bundle

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"ops-platform/internal/datascope"
)

// RegisteredSource is the fixed public API projection used by the installer.
// Query qualification remains the responsibility of the source adapters.
type RegisteredSource struct {
	SourceID           uuid.UUID         `json:"sourceId"`
	TenantID           uuid.UUID         `json:"tenantId"`
	SourceType         string            `json:"sourceType"`
	ClusterUID         string            `json:"clusterUid"`
	BackendLogicalID   string            `json:"backendLogicalId"`
	Revision           int64             `json:"revision"`
	CredentialRevision int64             `json:"credentialRevision"`
	Status             string            `json:"status"`
	DataScopeMapping   datascope.Mapping `json:"dataScopeMapping"`
}

// VerifyRegisteredBusinessSources checks the actual, tenant-authenticated API
// registrations before business activation. It grants no query capability.
func VerifyRegisteredBusinessSources(b BusinessValues, registrations []RegisteredSource) (BusinessValues, error) {
	if b.values == nil {
		return BusinessValues{}, errors.New("explicit business configuration required")
	}
	byID := map[string]RegisteredSource{}
	for _, s := range registrations {
		id := s.SourceID.String()
		if _, exists := byID[id]; exists {
			return BusinessValues{}, errors.New("duplicate API source identity")
		}
		byID[id] = s
	}
	check := func(id, tenant, kind, cluster, backend string, revision any) (RegisteredSource, error) {
		s, ok := byID[id]
		if !ok || s.SourceID == uuid.Nil || s.SourceID.Version() != 7 || s.TenantID.String() != tenant || s.SourceType != kind || s.ClusterUID != cluster || s.BackendLogicalID != backend || s.Status != "active" || s.CredentialRevision < 1 || fmt.Sprint(s.Revision) != fmt.Sprint(revision) {
			return s, errors.New("registered source identity, tenant, type, cluster, backend, status or revision differs")
		}
		return s, nil
	}
	sp04 := object(b.values, "sp04")
	for _, item := range sp04["clusters"].([]any) {
		c := item.(map[string]any)
		s, err := check(textValue(c, "SourceID"), textValue(c, "Tenant"), "kubernetes", textValue(c, "ClusterUID"), textValue(c, "BackendLogicalID"), c["SourceRevision"])
		if err != nil {
			return BusinessValues{}, err
		}
		// Kubernetes identity projection accepts only this registered cluster
		// and explicit namespaces. The actual public registration supplies the
		// source scope; an absent declaration cannot become a broad default.
		mapping := s.DataScopeMapping
		if mapping.NativeTenant != "" || len(mapping.Scopes["cluster"]) != 1 || mapping.Scopes["cluster"][0] != s.ClusterUID || len(mapping.Scopes["namespace"]) == 0 {
			return BusinessValues{}, errors.New("registered Kubernetes source lacks an explicit supported cluster/namespace scope")
		}
		for dimension, values := range mapping.Scopes {
			if (dimension != "cluster" && dimension != "namespace") || len(values) == 0 {
				return BusinessValues{}, errors.New("registered Kubernetes source isolation dimension unsupported")
			}
			for _, value := range values {
				if value == "" {
					return BusinessValues{}, errors.New("registered Kubernetes source scope is empty")
				}
			}
		}
	}
	for _, item := range sp04["sources"].([]any) {
		binding := object(item.(map[string]any), "Binding")
		mapping := object(binding, "ScopeMapping")
		clusters := object(mapping, "scopes")["cluster"].([]any)
		if len(clusters) != 1 {
			return BusinessValues{}, errors.New("current source registration requires one explicit cluster")
		}
		s, err := check(textValue(binding, "SourceID"), textValue(binding, "Tenant"), textValue(binding, "SourceType"), clusters[0].(string), textValue(binding, "BackendLogicalID"), binding["Revision"])
		if err != nil {
			return BusinessValues{}, err
		}
		raw, _ := json.Marshal(s.DataScopeMapping)
		var actual map[string]any
		if json.Unmarshal(raw, &actual) != nil || jsonDigest(actual) != jsonDigest(mapping) {
			return BusinessValues{}, errors.New("registered source mapping differs from the explicit installation scope")
		}
	}
	b.registrationVerified = true
	return b, nil
}

// The planned and API-assigned IDs may differ only at the explicit activation
// boundary. Replacing each ID by its semantic binding preserves ingestion
// associations; tenant, scope, revision, endpoint, trust and budget stay bound.
func registeredBusinessInstallationDigest(b BusinessValues) string {
	raw, _ := json.Marshal(b.values)
	var snapshot map[string]any
	if json.Unmarshal(raw, &snapshot) != nil {
		return ""
	}
	ids := map[string]string{}
	sp04 := object(snapshot, "sp04")
	for _, item := range sp04["clusters"].([]any) {
		c := item.(map[string]any)
		key := textValue(c, "Tenant") + "/kubernetes/" + textValue(c, "BackendLogicalID")
		ids[textValue(c, "SourceID")] = key
		c["SourceID"] = key
	}
	for _, item := range sp04["sources"].([]any) {
		s := item.(map[string]any)
		c := object(s, "Binding")
		key := textValue(c, "Tenant") + "/" + textValue(c, "SourceType") + "/" + textValue(c, "BackendLogicalID")
		ids[textValue(c, "SourceID")] = key
		c["SourceID"] = key
	}
	for _, item := range object(snapshot, "sp05")["ingestionBindings"].([]any) {
		c := item.(map[string]any)
		if key, ok := ids[textValue(c, "SourceID")]; ok {
			c["SourceID"] = key
		}
	}
	return businessInstallationDigest(BusinessValues{values: snapshot})
}
