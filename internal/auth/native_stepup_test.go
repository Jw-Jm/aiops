package auth

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The verified-proof marker is how an asynchronous stage tells the policy
// decision that the frozen proof was already re-proved in this transaction. It
// is context-only: a request header can never reach it.
func TestVerifiedNativeStepUpMarker(t *testing.T) {
	proof := NativeProof{SessionID: uuid.Must(uuid.NewV7()), Ref: uuid.Must(uuid.NewV7()),
		Operation: "command_execution", BindingDigest: []byte{1, 2, 3}, ExpiresAt: time.Now().UTC().Add(time.Minute)}
	if _, ok := VerifiedNativeStepUpFromContext(context.Background()); ok {
		t.Fatal("an empty context carried a verified proof")
	}
	ctx := WithVerifiedNativeStepUp(context.Background(), proof)
	got, ok := VerifiedNativeStepUpFromContext(ctx)
	if !ok || got.Ref != proof.Ref || got.Operation != proof.Operation || string(got.BindingDigest) != string(proof.BindingDigest) {
		t.Fatalf("verified proof = %+v", got)
	}
	// The marker does not survive an unrelated context.
	if _, ok := VerifiedNativeStepUpFromContext(context.WithValue(ctx, struct{ name string }{"other"}, "x")); !ok {
		t.Fatal("the marker was lost")
	}
}

// The intent is bound by the business boundary and read back by the same
// boundary; the request path can never set it.
func TestNativeStepUpIntentRoundTrip(t *testing.T) {
	if _, ok := NativeStepUpIntentFromContext(context.Background()); ok {
		t.Fatal("an empty context carried a step-up intent")
	}
	intent := NativeStepUpIntent{ChallengeID: uuid.Must(uuid.NewV7()), Operation: "command_execution", Target: json.RawMessage(`{"kind":"k8s_object"}`)}
	got, ok := NativeStepUpIntentFromContext(WithNativeStepUpIntent(context.Background(), intent))
	if !ok || got.ChallengeID != intent.ChallengeID || got.Operation != intent.Operation || string(got.Target) != string(intent.Target) {
		t.Fatalf("intent = %+v", got)
	}
}
