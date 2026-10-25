package action

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"ops-platform/internal/auth"
	"ops-platform/internal/identity"
)

func stepUpBinding() Binding {
	namespace := "apps"
	return Binding{
		DigestVersion: "execution-request/v1", TenantID: uuid.Must(uuid.NewV7()),
		Subject: "user:step-up", IncidentID: uuid.Must(uuid.NewV7()),
		Target: "k8s+v1://tenant/cluster/core/Node/node-a", TargetUID: "node-a",
		TargetRevision: 2, ClusterUID: "cluster", Namespace: &namespace, Shell: "bash",
		CommandDigest: "sha256:command", ProfileID: uuid.Must(uuid.NewV7()), ProfileVersion: 1,
		Options:       Options{TimeoutSeconds: 60, MaxOutputBytes: 4096, Environment: map[string]string{}},
		PolicyVersion: uuid.Must(uuid.NewV7()).String(), RiskVersion: RiskVersion,
	}
}

// The acknowledgement and the preparation must derive the same step-up target:
// the client supplies only the challenge reference, never the target.
func TestCommandStepUpContextDerivesTheSameTarget(t *testing.T) {
	binding := stepUpBinding()
	assessment := uuid.Must(uuid.NewV7())
	session := identity.ActiveSession{
		TenantID: binding.TenantID, UserID: uuid.Must(uuid.NewV7()), SessionID: uuid.Must(uuid.NewV7()),
		Subject: binding.Subject, Mode: "full", SessionVersion: 2, CredentialVersion: 3, AuthorizationVersion: 4,
	}
	challenge := uuid.Must(uuid.NewV7())
	ctx := auth.WithNativeSession(context.Background(), auth.NativeSessionContext{Session: session, StepUpChallengeID: challenge})
	bound := commandStepUpContext(ctx, assessment, binding)
	intent, ok := auth.NativeStepUpIntentFromContext(bound)
	if !ok {
		t.Fatal("the command boundary did not bind a native step-up intent")
	}
	if intent.ChallengeID != challenge || intent.Operation != CommandStepUpOperation {
		t.Fatalf("intent = %+v", intent)
	}
	expected, _ := json.Marshal(NativeCommandStepUpTarget(binding, assessment))
	if string(intent.Target) != string(expected) {
		t.Fatalf("intent target = %s want %s", intent.Target, expected)
	}
	// A request without a native session never carries an intent: a legacy
	// request must not be able to look native.
	plain := commandStepUpContext(context.Background(), assessment, binding)
	if _, ok := auth.NativeStepUpIntentFromContext(plain); ok {
		t.Fatal("a request without a native session bound a native step-up intent")
	}
}

// The marker that an asynchronous stage sets after re-proving the frozen proof is
// only accepted for the command operation, with a complete binding digest and
// inside the proof's own expiry.
func TestVerifiedStepUpProof(t *testing.T) {
	now := time.Now().UTC()
	valid := auth.NativeProof{SessionID: uuid.Must(uuid.NewV7()), Ref: uuid.Must(uuid.NewV7()),
		Operation: CommandStepUpOperation, BindingDigest: make([]byte, 32), ExpiresAt: now.Add(time.Minute)}
	if err := verifiedStepUpProof(valid, now); err != nil {
		t.Fatalf("valid proof: %v", err)
	}
	cases := map[string]auth.NativeProof{
		"other operation":  {Operation: "mfa_enrollment", BindingDigest: make([]byte, 32), ExpiresAt: now.Add(time.Minute)},
		"empty digest":     {Operation: CommandStepUpOperation, ExpiresAt: now.Add(time.Minute)},
		"short digest":     {Operation: CommandStepUpOperation, BindingDigest: make([]byte, 31), ExpiresAt: now.Add(time.Minute)},
		"expired":          {Operation: CommandStepUpOperation, BindingDigest: make([]byte, 32), ExpiresAt: now.Add(-time.Second)},
		"missing expiry":   {Operation: CommandStepUpOperation, BindingDigest: make([]byte, 32)},
		"empty everything": {},
	}
	for name, proof := range cases {
		if err := verifiedStepUpProof(proof, now); !errors.Is(err, auth.ErrStepUpInvalid) {
			t.Fatalf("%s proof = %v", name, err)
		}
	}
}

// An execution without a frozen proof, with a proof for another operation, or
// with an expired proof can never be dispatched: the re-proof is refused before
// any database work.
func TestReproveFrozenStepUpRefusesIncompleteProof(t *testing.T) {
	now := time.Now().UTC()
	execution := Execution{Binding: stepUpBinding(), Authority: Authority{Source: auth.AuthoritySourceNative, UserID: uuid.Must(uuid.NewV7()), AuthorizationVersion: 1}}
	service := Service{}
	if _, err := service.reproveFrozenStepUp(context.Background(), nil, execution); !errors.Is(err, ErrDenied) {
		t.Fatalf("missing proof = %v", err)
	}
	execution.stepUp = &frozenStepUp{SessionID: uuid.Must(uuid.NewV7()), ProofID: uuid.Must(uuid.NewV7()),
		Operation: "mfa_enrollment", BindingDigest: make([]byte, 32), ExpiresAt: now.Add(time.Minute)}
	if _, err := service.reproveFrozenStepUp(context.Background(), nil, execution); !errors.Is(err, ErrDenied) {
		t.Fatalf("foreign operation = %v", err)
	}
	execution.stepUp.Operation = CommandStepUpOperation
	execution.stepUp.BindingDigest = make([]byte, 31)
	if _, err := service.reproveFrozenStepUp(context.Background(), nil, execution); !errors.Is(err, ErrDenied) {
		t.Fatalf("short digest = %v", err)
	}
	execution.stepUp.BindingDigest = make([]byte, 32)
	execution.stepUp.ExpiresAt = now.Add(-time.Second)
	if _, err := service.reproveFrozenStepUp(context.Background(), nil, execution); !errors.Is(err, ErrDenied) {
		t.Fatalf("expired proof = %v", err)
	}
	// A legacy execution never has a native proof to re-prove.
	execution.stepUp.ExpiresAt = now.Add(time.Minute)
	execution.Authority = Authority{Source: auth.AuthoritySourceLegacy}
	if _, err := service.reproveFrozenStepUp(context.Background(), nil, execution); !errors.Is(err, ErrDenied) {
		t.Fatalf("legacy authority = %v", err)
	}
}
