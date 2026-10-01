package source

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestSourceEnvelopeCannotExpandRegisteredScope(t *testing.T) {
	registered := SourceRegistration{
		TenantID:   uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987330"),
		SourceID:   uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987331"),
		SourceType: "victoriametrics", InstanceKey: "vm-prod-a",
		AllowedSchemas: []string{"finding-envelope/v1"},
		ClusterID:      uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987332"), ClusterUID: "cluster-prod-a",
		AuthRef: "openbao://kv/platform/sources/vm-prod-a", CredentialRevision: 2, Status: "active", Revision: 3,
	}
	identity := SourceIdentity{TenantID: registered.TenantID, SourceID: registered.SourceID, CredentialRevision: 2}
	envelope := FindingEnvelope{
		SchemaVersion: "finding-envelope/v1",
		TenantID:      registered.TenantID, ClusterUID: registered.ClusterUID,
		Source: EnvelopeSource{System: registered.SourceType, Instance: registered.InstanceKey},
	}
	verifier := CredentialVerifierFunc(func(_ context.Context, authRef string, got SourceIdentity, _ FindingEnvelope) error {
		if authRef != registered.AuthRef || got.CredentialRevision != registered.CredentialRevision {
			return ErrUnauthorized
		}
		return nil
	})

	bound, err := bindEnvelope(context.Background(), registered, identity, envelope, verifier)
	if err != nil {
		t.Fatalf("valid source envelope rejected: %v", err)
	}
	if bound.TenantID != registered.TenantID || bound.ClusterID != registered.ClusterID || bound.ClusterUID != registered.ClusterUID || bound.SourceID != registered.SourceID {
		t.Fatalf("bound context did not come from the registration: %#v", bound)
	}

	for name, mutate := range map[string]func(*SourceIdentity, *FindingEnvelope, *SourceRegistration){
		"unregistered-schema": func(_ *SourceIdentity, input *FindingEnvelope, _ *SourceRegistration) {
			if err := json.Unmarshal([]byte(`{"schemaVersion":"finding-envelope/v999"}`), input); err != nil {
				t.Fatal(err)
			}
		},
		"tenant": func(_ *SourceIdentity, input *FindingEnvelope, _ *SourceRegistration) { input.TenantID = uuid.New() },
		"cluster": func(_ *SourceIdentity, input *FindingEnvelope, _ *SourceRegistration) {
			input.ClusterUID = "cluster-other"
		},
		"source-system": func(_ *SourceIdentity, input *FindingEnvelope, _ *SourceRegistration) {
			input.Source.System = "deepflow"
		},
		"source-instance": func(_ *SourceIdentity, input *FindingEnvelope, _ *SourceRegistration) {
			input.Source.Instance = "vm-other"
		},
		"stale-credential": func(got *SourceIdentity, _ *FindingEnvelope, _ *SourceRegistration) { got.CredentialRevision-- },
		"disabled-source":  func(_ *SourceIdentity, _ *FindingEnvelope, source *SourceRegistration) { source.Status = "disabled" },
		"rotated-source":   func(_ *SourceIdentity, _ *FindingEnvelope, source *SourceRegistration) { source.Status = "rotated" },
	} {
		t.Run(name, func(t *testing.T) {
			gotIdentity, gotEnvelope, gotSource := identity, envelope, registered
			mutate(&gotIdentity, &gotEnvelope, &gotSource)
			if _, err := bindEnvelope(context.Background(), gotSource, gotIdentity, gotEnvelope, verifier); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("scope or stale credential was accepted: %v", err)
			}
		})
	}
}

func TestRegisterRequiresAuthRefRatherThanCredentialMaterial(t *testing.T) {
	clusterID := uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987332")
	base := RegisterCommand{
		AllowedSchemas: []string{"finding-envelope/v1"},
		SourceType:     "victorialogs", InstanceKey: "logs-prod-a",
		ClusterID: &clusterID,
		AuthRef:   "openbao://kv/platform/sources/logs-prod-a",
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid source registration rejected: %v", err)
	}
	for _, invalid := range []string{"password-value", "https://vault/path", "openbao://kv/../secret", "openbao://"} {
		t.Run(invalid, func(t *testing.T) {
			input := base
			input.AuthRef = invalid
			if err := input.Validate(); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("invalid auth_ref %q accepted: %v", invalid, err)
			}
		})
	}
	for _, forbidden := range []string{"kubevirt", "cdi", "virtualization"} {
		t.Run("source-type-"+forbidden, func(t *testing.T) {
			input := base
			input.SourceType = forbidden
			if err := input.Validate(); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("unapproved source type %q accepted: %v", forbidden, err)
			}
		})
	}
}

func TestClusterIdentityCannotBeSilentlyRedefined(t *testing.T) {
	registered := ClusterRegistration{
		TenantID:   uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987330"),
		ClusterID:  uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987332"),
		ClusterUID: "cluster-prod-a", DisplayName: "Production A", Status: "active", Revision: 1,
	}
	command := ClusterCommand{ClusterUID: "cluster-prod-a", DisplayName: "Production A"}
	if !sameClusterIdentity(registered, command) {
		t.Fatal("same tenant cluster identity was not recognized for idempotent registration")
	}
	command.ClusterUID = "cluster-other"
	if sameClusterIdentity(registered, command) {
		t.Fatal("clusterUid redefinition was treated as the same cluster")
	}
	command.ClusterUID = registered.ClusterUID
	command.DisplayName = "Different Cluster"
	if sameClusterIdentity(registered, command) {
		t.Fatal("cluster rename silently redefined a registered identity")
	}
}

func TestRegistrationRequiresBoundedSchemaAndClusterMetadata(t *testing.T) {
	for _, schemas := range [][]string{nil, {}, {"finding-envelope/v999"}, {"finding-envelope/v1", "finding-envelope/v1"}} {
		if validAllowedSchemas(schemas) {
			t.Fatalf("invalid schema scope accepted: %v", schemas)
		}
	}
	base := ClusterCommand{ClusterUID: "review", DisplayName: "Review", APIEndpointRef: "openbao://kv/clusters/review/endpoint", Distribution: "orbstack", ActualVersions: map[string]string{"kubernetes": "v1.35.6+orb1"}, Capabilities: map[string]bool{"kubernetes": true}}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, capability := range []string{"kubevirt", "CDI", "VM", "VMI", "DataVolume"} {
		changed := base
		changed.Capabilities = map[string]bool{capability: true}
		if err := changed.Validate(); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("deferred capability %s admitted: %v", capability, err)
		}
	}
	changed := base
	changed.ActualVersions = map[string]string{"kubernetes": "latest"}
	if err := changed.Validate(); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("unlocked actual Kubernetes version admitted")
	}
}
