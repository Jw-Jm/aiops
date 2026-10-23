package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/auth"
	"ops-platform/internal/identity"
)

// The path, method and canonical body bind different mutations of the same
// object separately. The high-risk write recomputes them before the ledger.
func nativeHTTPWriteIntent(r *http.Request, tx pgx.Tx, action string) (auth.NativeStepUpIntent, error) {
	native, ok := auth.NativeSessionFromContext(r.Context())
	if !ok {
		return auth.NativeStepUpIntent{}, auth.ErrStepUpInvalid
	}
	if len(r.Header.Values("X-Ops-Step-Up-Challenge")) != 1 {
		return auth.NativeStepUpIntent{}, auth.ErrStepUpInvalid
	}
	id, err := uuid.Parse(r.Header.Get("X-Ops-Step-Up-Challenge"))
	if err != nil {
		return auth.NativeStepUpIntent{}, auth.ErrStepUpInvalid
	}
	var operation string
	var target json.RawMessage
	err = tx.QueryRow(r.Context(), `SELECT out_operation,out_target FROM identity.step_up_challenge_get($1,$2,$3)`, native.Session.TenantID, native.Session.UserID, id).Scan(&operation, &target)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.NativeStepUpIntent{}, auth.ErrStepUpInvalid
	}
	if err != nil {
		return auth.NativeStepUpIntent{}, err
	}
	expected := ""
	switch {
	case strings.Contains(action, "source"):
		expected = "source_administration"
	case action == "create-cluster" || action == "onboard-host":
		expected = "cluster_administration"
	case strings.Contains(action, "registry") || action == "publish-policy-bundle":
		expected = "registry_administration"
	case strings.Contains(action, "execution-profile"):
		expected = "execution_profile_administration"
	case strings.Contains(action, "role-binding") || strings.HasPrefix(action, "native-user-") || action == "create-tenant":
		expected = "user_administration"
	}
	if expected == "" || operation != expected {
		return auth.NativeStepUpIntent{}, auth.ErrStepUpInvalid
	}
	var data struct {
		Kind           string `json:"kind"`
		Revision       int    `json:"revision"`
		RegistrationID string `json:"registrationId"`
		ObjectID       string `json:"objectId"`
		Write          struct {
			Method     string `json:"method"`
			Path       string `json:"path"`
			BodyDigest string `json:"bodyDigest"`
		} `json:"write"`
	}
	if json.Unmarshal(target, &data) != nil {
		return auth.NativeStepUpIntent{}, auth.ErrStepUpInvalid
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxIdempotentRequestBody+1))
	r.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil || len(body) > maxIdempotentRequestBody {
		return auth.NativeStepUpIntent{}, auth.ErrStepUpInvalid
	}
	var value any
	var encoded []byte
	if len(body) > 0 {
		if json.Unmarshal(body, &value) != nil {
			return auth.NativeStepUpIntent{}, auth.ErrStepUpInvalid
		}
		encoded, err = nativeBoundWriteBody(native.CSRFToken, r.Method, r.URL.Path, body)
		if err != nil {
			return auth.NativeStepUpIntent{}, auth.ErrStepUpInvalid
		}
	}
	if data.Write.Method != r.Method || data.Write.Path != r.URL.EscapedPath() || data.Write.BodyDigest != "sha256:"+identity.DigestHex(identity.DigestSHA256(encoded)) || r.URL.RawQuery != "" {
		return auth.NativeStepUpIntent{}, auth.ErrStepUpInvalid
	}
	if sourceID := chi.URLParam(r, "sourceId"); data.Kind != "administrative_write" && sourceID != "" && sourceID != data.RegistrationID {
		return auth.NativeStepUpIntent{}, auth.ErrStepUpInvalid
	}
	if v, ok := value.(map[string]any); ok {
		if revision, exists := v["expectedRevision"]; exists && data.Kind != "administrative_write" && revision != float64(data.Revision) {
			return auth.NativeStepUpIntent{}, auth.ErrStepUpInvalid
		}
	}
	return auth.NativeStepUpIntent{ChallengeID: id, Operation: operation, Target: target}, nil
}
