package source

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestSourceEnvelopeCannotExpandRegisteredScope(t *testing.T) {
	registered := SourceRegistration{
		TenantID:   uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987330"),
		SourceID:   uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987331"),
		SourceType: "victoriametrics", InstanceKey: "vm-prod-a",
		ClusterID: uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987332"), ClusterUID: "cluster-prod-a",
		AuthRef: "openbao://kv/platform/sources/vm-prod-a", CredentialRevision: 2, Status: "active", Revision: 3,
	}
	identity := SourceIdentity{TenantID: registered.TenantID, SourceID: registered.SourceID, CredentialRevision: 2}
	envelope := FindingEnvelope{
		TenantID: registered.TenantID, ClusterUID: registered.ClusterUID,
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
		SourceType: "victorialogs", InstanceKey: "logs-prod-a",
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
