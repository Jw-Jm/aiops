package httpapi

import (
	"context"

	"github.com/google/uuid"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
)

// nativeProofFromContext returns the native reference for an internal signed
// context. It reads only the trusted middleware context: an anonymous or legacy
// caller yields nil and therefore a legacy claim. A client cannot supply this.
func nativeProofFromContext(ctx context.Context) *graph.NativeProof {
	if ctx == nil {
		return nil
	}
	native, ok := auth.NativeSessionFromContext(ctx)
	if !ok || native.Session.SessionID == uuid.Nil {
		return nil
	}
	return &graph.NativeProof{SessionID: native.Session.SessionID.String()}
}

// internalProofFromContext chooses the authoritative source for a signed
// internal context: a live native session, else a persisted native task, else a
// genuinely legacy caller. An empty result is legacy only because neither trusted
// source is present - it is never a substitute for a broken native proof.
func internalProofFromContext(ctx context.Context) *graph.NativeProof {
	if proof := nativeProofFromContext(ctx); proof != nil {
		return proof
	}
	if jobID, ok := graph.TaskAuthorityFromContext(ctx); ok {
		return &graph.NativeProof{JobID: jobID.String()}
	}
	return nil
}

// nativeTaskProof returns the persisted-task reference for a call made on behalf
// of an assistant job that was admitted by a native account. The authority itself
// is not in this proof: the Worker reloads it from the persisted job row.
func nativeTaskProof(j investigation.Job) *graph.NativeProof {
	if j.Assistant == nil || j.JobID == uuid.Nil {
		return nil
	}
	if j.Assistant.AuthorizationSource != investigation.AuditSourceNative {
		return nil
	}
	return &graph.NativeProof{JobID: j.JobID.String()}
}

// internalClaims stamps the explicit source on a signed internal context. A nil
// proof means a legacy caller; there is no third case and no inference.
func internalClaims(proof *graph.NativeProof, claims graph.Claims) graph.Claims {
	if proof == nil {
		claims.Source = graph.SourceLegacy
		claims.Native = nil
		return claims
	}
	claims.Native = proof
	if proof.JobID != "" {
		claims.Source = graph.SourceNativeTask
		return claims
	}
	claims.Source = graph.SourceNativeInteractive
	return claims
}
