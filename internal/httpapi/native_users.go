package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"ops-platform/internal/audit"
	"ops-platform/internal/auth"
	"ops-platform/internal/contract"
	"ops-platform/internal/identity"
	"ops-platform/internal/persistence"
)

type NativeUserHandlers struct{ Pool persistence.TxBeginner }

func writeNativeAdminError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := http.StatusInternalServerError, "INTERNAL"
	message := "native user operation was rejected"
	var database *pgconn.PgError
	switch {
	case errors.Is(err, identity.ErrSessionInvalid), errors.Is(err, auth.ErrForbidden):
		status, code = 403, "FORBIDDEN"
	case errors.Is(err, auth.ErrStepUpInvalid):
		status, code = 403, "STEP_UP_REQUIRED"
	case errors.As(err, &database):
		switch database.Code {
		case "42501", "28000":
			status, code = 403, "FORBIDDEN"
		case "40001", "23505":
			status, code = 409, "CONFLICT"
		case "22023", "22P02", "23514", "23502":
			status, code = 400, "INVALID_ARGUMENT"
		case "55000":
			status, code, message = 409, "CONFLICT", "没有匹配的已核对清单，请先完成受控导入并核对目标账号与清单摘要。"
		case "P0002":
			status, code = 404, "NOT_FOUND"
		}
	}
	writeTenantError(w, status, code, message, false, nativeRequestID(r))
}
func (h NativeUserHandlers) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	native, ok := nativeMFASession(w, r)
	if !ok {
		return
	}
	if !auth.HasRole(r.Context(), auth.PlatformAdmin) {
		writeNativeAdminError(w, r, auth.ErrForbidden)
		return
	}
	if r.Method == http.MethodGet && (r.URL.Path == "/api/v1/admin/users" || r.URL.Path == adminRoleBindingsPath) {
		h.list(w, r, native)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/v1/admin/subject-mappings" {
		h.listMappings(w, r, native)
		return
	}
	operation, schema, target := "", "", uuid.Nil
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.URL.Path == "/api/v1/admin/users" && r.Method == http.MethodPost:
		operation, schema, target = "create", "accountCreateRequest", uuid.Must(uuid.NewV7())
	case len(parts) == 5 && parts[2] == "admin" && parts[3] == "users" && r.Method == http.MethodPatch:
		operation, schema = "update", "accountUpdateRequest"
		target, _ = uuid.Parse(parts[4])
	case len(parts) == 6 && parts[2] == "admin" && parts[3] == "users" && r.Method == http.MethodPost:
		target, _ = uuid.Parse(parts[4])
		switch parts[5] {
		case "password:reset":
			operation, schema = "password_reset", "adminPasswordResetRequest"
		case "sessions:revoke":
			operation, schema = "sessions_revoke", "sessionRevocationRequest"
		}
	case r.URL.Path == "/api/v1/admin/subject-mappings" && r.Method == http.MethodPost:
		operation, schema = "map_subject", "subjectMappingRequest"
	case r.URL.Path == adminRoleBindingsPath && r.Method == http.MethodPost:
		operation, schema = "grant", "roleGrantRequest"
	case len(parts) == 6 && parts[2] == "admin" && parts[3] == "role-bindings" && parts[5] == "status" && r.Method == http.MethodPatch:
		operation, schema = "revoke_grant", "roleGrantStatusRequest"
	}
	if operation == "" {
		writeTenantError(w, 404, "NOT_FOUND", "endpoint is unavailable", false, nativeRequestID(r))
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxIdempotentRequestBody+1))
	if err != nil || len(raw) > maxIdempotentRequestBody {
		writeTenantError(w, 400, "INVALID_ARGUMENT", "request body is invalid", false, nativeRequestID(r))
		return
	}
	if err = contract.Validate("https://ops.local/schemas/ui-v12-identity/v1#/$defs/"+schema, raw); err != nil {
		writeTenantError(w, 400, "INVALID_ARGUMENT", "native user request is invalid", false, nativeRequestID(r))
		return
	}
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil {
		writeTenantError(w, 400, "INVALID_ARGUMENT", "request body is invalid", false, nativeRequestID(r))
		return
	}
	if operation == "grant" || operation == "revoke_grant" || operation == "map_subject" {
		target, _ = uuid.Parse(body["userId"].(string))
		delete(body, "userId")
	}
	if target == uuid.Nil {
		writeTenantError(w, 400, "INVALID_ARGUMENT", "target user is invalid", false, nativeRequestID(r))
		return
	}
	if operation == "revoke_grant" {
		binding, err := uuid.Parse(parts[4])
		if err != nil {
			writeTenantError(w, 400, "INVALID_ARGUMENT", "binding is invalid", false, nativeRequestID(r))
			return
		}
		body["bindingId"] = binding.String()
		body["expectedAuthorizationVersion"] = body["expectedRevision"]
		delete(body, "expectedRevision")
		delete(body, "status")
	}
	if operation == "create" {
		login := identity.NormalizeLoginName(body["loginName"].(string))
		if identity.ValidateLoginName(login) != nil {
			writeTenantError(w, 400, "INVALID_ARGUMENT", "login name is invalid", false, nativeRequestID(r))
			return
		}
		body["loginName"] = login
	}
	if password, ok := body["initialPassword"].(string); ok && identity.ValidatePassword(password) != nil {
		writeTenantError(w, 400, "INVALID_ARGUMENT", "initial password is invalid", false, nativeRequestID(r))
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if len(r.Header.Values("Idempotency-Key")) != 1 || !validIdempotencyKey(key) {
		writeTenantError(w, 400, "INVALID_ARGUMENT", "one valid Idempotency-Key is required", false, nativeRequestID(r))
		return
	}
	canonical, err := nativeBoundWriteBody(native.CSRFToken, r.Method, r.URL.Path, raw)
	if err != nil {
		writeTenantError(w, 400, "INVALID_ARGUMENT", "request body is invalid", false, nativeRequestID(r))
		return
	}
	actor, _ := auth.RequestContextFromContext(r.Context())
	var response persistence.StoredResponse
	err = persistence.WithTenantTx(r.Context(), h.Pool, native.Session.TenantID, func(tx pgx.Tx) error {
		if err := identity.LockAdminAccounts(r.Context(), tx, native.Session, target); err != nil {
			return err
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		intent, err := nativeHTTPWriteIntent(r, tx, "native-user-"+operation)
		if err != nil {
			return err
		}
		ctx := auth.WithNativeStepUpIntent(r.Context(), intent)
		if _, err := auth.TouchCurrentStepUpSession(ctx, tx, actor, []string{auth.StepUpACRLevel2}); err != nil {
			return err
		}
		decision, err := persistence.Begin(ctx, tx, persistence.Scope{TenantID: actor.TenantID, Subject: actor.Subject, Operation: "native-user-" + operation + ":" + r.URL.Path}, key, persistence.Digest("sha256:"+identity.DigestHex(identity.DigestSHA256(canonical))))
		if err != nil {
			return err
		}
		if decision.Kind == persistence.DecisionReplay {
			response = decision.Response
			return nil
		}
		if decision.Kind != persistence.DecisionProceed {
			return &pgconn.PgError{Code: "40001"}
		}
		if password, ok := body["initialPassword"].(string); ok {
			hashed, err := identity.HashPassword(password)
			if err != nil {
				return err
			}
			delete(body, "initialPassword")
			body["passwordHash"] = hashed
		}
		payload, _ := json.Marshal(body)
		var result json.RawMessage
		if operation == "map_subject" {
			result, err = identity.AdminMapSubject(ctx, tx, native.Session, target, payload)
		} else {
			result, err = identity.AdminWrite(ctx, tx, native.Session, target, operation, payload)
		}
		if err != nil {
			return err
		}
		var data map[string]any
		if err = json.Unmarshal(result, &data); err != nil {
			return err
		}
		status := http.StatusOK
		if data["code"] == "last_active_administrator" {
			status = http.StatusConflict
		} else {
			auditID := uuid.Must(uuid.NewV7())
			change := map[string]any{}
			for _, field := range []string{"loginName", "displayName", "status", "role", "scope", "reason", "bindingId", "expectedRevision", "expectedAuthorizationVersion", "expectedSessionVersion", "legacySubject", "basis", "evidenceDigest", "expectedUserRevision"} {
				if value, ok := body[field]; ok {
					change[field] = value
				}
			}
			if version, ok := body["expectedCredentialVersion"]; ok {
				change["expectedPasswordVersion"] = version
			}
			if operation == "grant" {
				change["bindingId"] = data["bindingId"]
			}
			if _, err = audit.Append(ctx, tx, audit.Entry{TenantID: actor.TenantID, RecordID: auditID, EventType: "identity.admin." + operation, EntityKind: "native_user", EntityID: target, Subject: actor.Subject, Payload: map[string]any{"targetUserId": target.String(), "operation": operation, "change": change, "authoritySource": "native", "actorUserId": native.Session.UserID.String(), "authorizationVersion": native.Session.AuthorizationVersion, "requestId": actor.RequestID}}); err != nil {
				return err
			}
			if operation == "password_reset" {
				data["auditRecordId"] = auditID.String()
			}
			if operation == "create" || operation == "grant" || operation == "map_subject" {
				status = http.StatusCreated
			}
		}
		envelope := apiSuccess(nativeRequestID(r), data)
		if status == http.StatusConflict {
			envelope = map[string]any{"code": "CONFLICT", "message": "不能停用或撤销最后一名有效管理员", "retryable": false, "requestId": nativeRequestID(r), "details": data}
		}
		encoded, err := json.Marshal(envelope)
		if err != nil {
			return err
		}
		response = persistence.StoredResponse{Status: status, ContentType: "application/json", Body: encoded}
		return persistence.Complete(ctx, tx, decision.Lease, response)
	})
	if err != nil {
		writeNativeAdminError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.Status)
	_, _ = w.Write(response.Body)
}

func (h NativeUserHandlers) list(w http.ResponseWriter, r *http.Request, native auth.NativeSessionContext) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			writeTenantError(w, 400, "INVALID_ARGUMENT", "limit is invalid", false, nativeRequestID(r))
			return
		}
		limit = value
	}
	var after *uuid.UUID
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeTenantError(w, 400, "INVALID_ARGUMENT", "cursor is invalid", false, nativeRequestID(r))
			return
		}
		after = &id
	}
	accounts, err := identity.AdminAccounts(r.Context(), h.Pool, native.Session, after, limit+1)
	if err != nil {
		writeNativeAdminError(w, r, err)
		return
	}
	meta := map[string]any{}
	if len(accounts) > limit {
		accounts = accounts[:limit]
		var last struct {
			UserID string `json:"userId"`
		}
		_ = json.Unmarshal(accounts[len(accounts)-1], &last)
		meta["nextCursor"] = last.UserID
	}
	if r.URL.Path == adminRoleBindingsPath {
		grants := []json.RawMessage{}
		for _, account := range accounts {
			var view struct {
				Grants []json.RawMessage `json:"roleGrants"`
			}
			if json.Unmarshal(account, &view) == nil {
				grants = append(grants, view.Grants...)
			}
		}
		accounts = grants
	}
	writeJSON(w, 200, map[string]any{"data": accounts, "meta": meta, "requestId": nativeRequestID(r)})
}

func (h NativeUserHandlers) listMappings(w http.ResponseWriter, r *http.Request, native auth.NativeSessionContext) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			writeTenantError(w, 400, "INVALID_ARGUMENT", "limit is invalid", false, nativeRequestID(r))
			return
		}
		limit = n
	}
	var userID, after *uuid.UUID
	if raw := r.URL.Query().Get("userId"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil {
			writeTenantError(w, 400, "INVALID_ARGUMENT", "userId is invalid", false, nativeRequestID(r))
			return
		}
		userID = &id
	}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil {
			writeTenantError(w, 400, "INVALID_ARGUMENT", "cursor is invalid", false, nativeRequestID(r))
			return
		}
		after = &id
	}
	var subject *string
	if value := r.URL.Query().Get("legacySubject"); value != "" {
		if len([]rune(value)) > 256 {
			writeTenantError(w, 400, "INVALID_ARGUMENT", "legacySubject is invalid", false, nativeRequestID(r))
			return
		}
		subject = &value
	}
	mappings, err := identity.AdminSubjectMappings(r.Context(), h.Pool, native.Session, userID, subject, after, limit+1)
	if err != nil {
		writeNativeAdminError(w, r, err)
		return
	}
	meta := map[string]any{}
	if len(mappings) > limit {
		mappings = mappings[:limit]
		var last struct {
			MappingID string `json:"mappingId"`
		}
		_ = json.Unmarshal(mappings[len(mappings)-1], &last)
		meta["nextCursor"] = last.MappingID
	}
	writeJSON(w, 200, map[string]any{"data": mappings, "meta": meta, "requestId": nativeRequestID(r)})
}
