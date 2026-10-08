package bundle

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"ops-platform/internal/datascope"
)

func registeredBusinessTest(t *testing.T) (BusinessValues, []RegisteredSource) {
	t.Helper()
	d := validBusinessDocument()
	var registrations []RegisteredSource
	sp04 := object(d, "sp04")
	for _, item := range sp04["clusters"].([]any) {
		c := item.(map[string]any)
		id := uuid.Must(uuid.NewV7())
		c["SourceID"] = id.String()
		registrations = append(registrations, RegisteredSource{SourceID: id, TenantID: uuid.MustParse(textValue(c, "Tenant")), SourceType: "kubernetes", ClusterUID: textValue(c, "ClusterUID"), BackendLogicalID: textValue(c, "BackendLogicalID"), Revision: 1, CredentialRevision: 1, Status: "active", DataScopeMapping: datascope.Mapping{Scopes: map[string][]string{"cluster": {textValue(c, "ClusterUID")}, "namespace": {"apps"}}}})
	}
	for _, item := range sp04["sources"].([]any) {
		b := object(item.(map[string]any), "Binding")
		id := uuid.Must(uuid.NewV7())
		b["SourceID"] = id.String()
		raw, _ := json.Marshal(b["ScopeMapping"])
		var scope datascope.Mapping
		json.Unmarshal(raw, &scope)
		registrations = append(registrations, RegisteredSource{SourceID: id, TenantID: uuid.MustParse(textValue(b, "Tenant")), SourceType: textValue(b, "SourceType"), ClusterUID: scope.Scopes["cluster"][0], BackendLogicalID: textValue(b, "BackendLogicalID"), Revision: 1, CredentialRevision: 1, Status: "active", DataScopeMapping: scope})
	}
	return readBusinessTest(t, d), registrations
}

func TestAPIRegisteredSourceIDsPreserveInstallationScopeAndRejectUnverifiedBindings(t *testing.T) {
	planned := readBusinessTest(t, validBusinessDocument())
	actual, registrations := registeredBusinessTest(t)
	if businessInstallationDigest(planned) == businessInstallationDigest(actual) || registeredBusinessInstallationDigest(planned) != registeredBusinessInstallationDigest(actual) {
		t.Fatal("API-assigned IDs did not preserve the planned semantic scope")
	}
	verified, err := VerifyRegisteredBusinessSources(actual, registrations)
	if err != nil || !verified.registrationVerified {
		t.Fatalf("actual registered bindings rejected: %v", err)
	}
	for name, mutate := range map[string]func([]RegisteredSource){
		"tenant":                   func(s []RegisteredSource) { s[0].TenantID = uuid.New() },
		"cluster":                  func(s []RegisteredSource) { s[0].ClusterUID = "replacement-cluster" },
		"backend":                  func(s []RegisteredSource) { s[0].BackendLogicalID = "foreign" },
		"revision":                 func(s []RegisteredSource) { s[0].Revision++ },
		"revoked":                  func(s []RegisteredSource) { s[0].Status = "disabled" },
		"credential":               func(s []RegisteredSource) { s[0].CredentialRevision = 0 },
		"wrong id":                 func(s []RegisteredSource) { s[0].SourceID = uuid.New() },
		"type":                     func(s []RegisteredSource) { s[0].SourceType = "deepflow" },
		"missing Kubernetes scope": func(s []RegisteredSource) { s[0].DataScopeMapping = datascope.Mapping{} },
		"expanded Kubernetes cluster": func(s []RegisteredSource) {
			s[0].DataScopeMapping.Scopes = map[string][]string{"cluster": {s[0].ClusterUID, "foreign"}, "namespace": {"apps"}}
		},
		"unsupported Kubernetes isolation": func(s []RegisteredSource) { s[0].DataScopeMapping.NativeTenant = "unverified-account" },
		"scope expansion": func(s []RegisteredSource) {
			s[1].DataScopeMapping.Scopes = map[string][]string{"cluster": {"current-cluster"}, "namespace": {"apps", "foreign"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			copy := append([]RegisteredSource(nil), registrations...)
			mutate(copy)
			if _, err := VerifyRegisteredBusinessSources(actual, copy); err == nil {
				t.Fatal("unsafe registration admitted")
			}
		})
	}
	if _, err := VerifyRegisteredBusinessSources(actual, registrations[1:]); err == nil {
		t.Fatal("missing registration admitted")
	}
	if _, err := VerifyRegisteredBusinessSources(actual, append(registrations, registrations[0])); err == nil {
		t.Fatal("duplicate registration admitted")
	}
}

func TestSourceIDTransitionCannotRebindIngestionOrExpandScope(t *testing.T) {
	d := validBusinessDocument()
	cluster := object(d, "sp04")["clusters"].([]any)[0].(map[string]any)
	object(d, "sp05")["ingestionBindings"] = []any{map[string]any{"Tenant": textValue(cluster, "Tenant"), "SourceID": textValue(cluster, "SourceID"), "SourceSystem": "kubernetes", "SourceInstance": "collector"}}
	// Digest comparisons use the already validated structure; the binding's
	// exact schema is separately enforced by ReadBusinessValues.
	b := BusinessValues{values: d}
	before := registeredBusinessInstallationDigest(b)
	cluster["SourceID"] = uuid.Must(uuid.NewV7()).String()
	if registeredBusinessInstallationDigest(b) == before {
		t.Fatal("stale ingestion SourceID silently rebound")
	}
	object(d, "sp05")["ingestionBindings"].([]any)[0].(map[string]any)["SourceID"] = cluster["SourceID"]
	if registeredBusinessInstallationDigest(b) != before {
		t.Fatal("consistent registered ID transition rejected")
	}
	cluster["ClusterUID"] = "foreign"
	if registeredBusinessInstallationDigest(b) == before {
		t.Fatal("cluster scope changed across registration boundary")
	}
}
