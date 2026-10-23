package contract_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	platformcontract "ops-platform/internal/contract"
)

// These tests validate the P0-03 versioned contracts for the V1.2 replication
// work. They are static: no database, Kubernetes, model endpoint or browser is
// contacted. They also cross-check the recorded evidence so a schema and its
// rejection list cannot drift apart silently.
const (
	uiV12IdentitySchema      = "https://ops.local/schemas/ui-v12-identity/v1"
	uiV12StepUpSchema        = "https://ops.local/schemas/ui-v12-stepup/v1"
	uiV12ObservabilitySchema = "https://ops.local/schemas/ui-v12-observability/v1"
	uiV12ModelSchema         = "https://ops.local/schemas/ui-v12-model/v1"

	uiV12Digest        = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	uiV12OtherDigest   = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
	uiV12ProfileRef    = "profile-v3@sha256:2222222222222222222222222222222222222222222222222222222222222222"
	uiV12UUID          = "11111111-1111-4111-8111-111111111111"
	uiV12OtherUUID     = "22222222-2222-4222-8222-222222222222"
	uiV12ThirdUUID     = "33333333-3333-4333-8333-333333333333"
	uiV12CanonicalID   = "k8s+v1://tenant-a/cluster-1/apps/Pod/pod-uid-1"
	uiV12Timestamp     = "2026-10-21T09:00:00Z"
	uiV12LaterStamp    = "2026-10-21T10:00:00Z"
	uiV12SessionExpiry = "2026-10-21T17:00:00Z"
)

type uiV12Case struct {
	name   string
	schema string
	base   string
	mutate func(*testing.T, map[string]any)
}

func TestUIV12SchemaFilesMatchEvidence(t *testing.T) {
	entries, err := filepath.Glob(filepath.Join("..", "..", "api", "schemas", "ui-v12-*.schema.json"))
	if err != nil {
		t.Fatalf("glob schemas: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("expected 4 ui-v12 schemas, found %d", len(entries))
	}
	actual := make(map[string]map[string]bool)
	for _, entry := range entries {
		raw, err := os.ReadFile(entry)
		if err != nil {
			t.Fatalf("read %s: %v", entry, err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("decode %s: %v", entry, err)
		}
		id, ok := doc["$id"].(string)
		if !ok || !strings.HasPrefix(id, "https://ops.local/schemas/ui-v12-") {
			t.Fatalf("%s has unexpected $id %v", entry, doc["$id"])
		}
		definitions, ok := doc["$defs"].(map[string]any)
		if !ok || len(definitions) == 0 {
			t.Fatalf("%s has no $defs", entry)
		}
		if _, ok := doc["not"]; !ok {
			t.Fatalf("%s must make the root document unusable so payloads are validated through #/$defs", entry)
		}
		names := make(map[string]bool, len(definitions))
		for name := range definitions {
			names[name] = true
		}
		actual[id] = names
	}

	inventory := uiV12Inventory(t)
	if len(inventory) != len(actual) {
		t.Fatalf("inventory lists %d schemas, found %d files", len(inventory), len(actual))
	}
	for id, names := range actual {
		recorded, ok := inventory[id]
		if !ok {
			t.Fatalf("inventory does not record %s", id)
		}
		if !sameStringSet(recorded, names) {
			t.Fatalf("inventory definitions for %s = %v, schema has %v", id, recorded, names)
		}
	}
}

func TestUIV12RootRejectsUnscopedPayloads(t *testing.T) {
	for _, schemaID := range []string{uiV12IdentitySchema, uiV12StepUpSchema, uiV12ObservabilitySchema, uiV12ModelSchema} {
		for _, payload := range []string{`{}`, `{"schemaVersion":"x"}`, `"text"`, `[]`, `null`} {
			if err := platformcontract.Validate(schemaID, []byte(payload)); err == nil {
				t.Fatalf("%s accepted unscoped payload %s; the contract must only be used through #/$defs fragments", schemaID, payload)
			}
		}
	}
}

func TestUIV12RejectionEvidenceMatchesCases(t *testing.T) {
	declared := uiV12Rejections(t)
	cases := uiV12RejectionCases()
	implemented := make(map[string]bool, len(cases))
	for _, test := range cases {
		if implemented[test.name] {
			t.Fatalf("duplicate rejection case %q", test.name)
		}
		if !declared[test.name] {
			t.Fatalf("rejection case %q is not recorded in evidence/p0-schema-rejections.json", test.name)
		}
		implemented[test.name] = true
	}
	for name := range declared {
		if !implemented[name] {
			t.Fatalf("evidence declares rejection case %q but no test implements it", name)
		}
	}
}

func TestUIV12PositiveContracts(t *testing.T) {
	for _, test := range uiV12PositiveCases() {
		test := test
		t.Run(test.name, func(t *testing.T) {
			if err := platformcontract.Validate(test.schema, []byte(test.base)); err != nil {
				t.Fatalf("valid payload rejected for %s: %v", test.schema, err)
			}
		})
	}
}

func TestUIV12RejectedContracts(t *testing.T) {
	for _, test := range uiV12RejectionCases() {
		test := test
		t.Run(test.name, func(t *testing.T) {
			// The baseline must be valid, otherwise the rejection would come
			// from an unrelated defect instead of the declared mutation.
			if err := platformcontract.Validate(test.schema, []byte(test.base)); err != nil {
				t.Fatalf("baseline payload is invalid before mutation: %v", err)
			}
			payload := uiV12Object(t, test.base)
			if test.mutate != nil {
				test.mutate(t, payload)
			}
			encoded, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("encode payload: %v", err)
			}
			if err := platformcontract.Validate(test.schema, encoded); err == nil {
				t.Fatalf("invalid payload accepted for %s (case %s)", test.schema, test.name)
			}
		})
	}
}

func uiV12PositiveCases() []uiV12Case {
	return []uiV12Case{
		{name: "login-request", schema: uiV12IdentitySchema + "#/$defs/loginRequest", base: `{
			"loginName": "alice", "password": "CorrectHorseBattery1", "deviceLabel": "desk-linux"}`},
		{name: "first-password-request", schema: uiV12IdentitySchema + "#/$defs/firstPasswordRequest", base: `{
			"currentPassword": "InitialPassword12", "newPassword": "CorrectHorseBattery1", "expectedCredentialVersion": 1}`},
		{name: "password-change-request", schema: uiV12IdentitySchema + "#/$defs/passwordChangeRequest", base: `{
			"currentPassword": "CorrectHorseBattery1", "newPassword": "AnotherCorrectBattery2", "expectedCredentialVersion": 4}`},
		{name: "account-create-request", schema: uiV12IdentitySchema + "#/$defs/accountCreateRequest", base: uiV12AccountCreateJSON()},
		{name: "account-update-display-name", schema: uiV12IdentitySchema + "#/$defs/accountUpdateRequest", base: uiV12AccountUpdateDisplayJSON()},
		{name: "account-update-status", schema: uiV12IdentitySchema + "#/$defs/accountUpdateRequest", base: `{
			"status": "disabled", "expectedRevision": 2}`},
		{name: "account-update-conflict", schema: uiV12IdentitySchema + "#/$defs/accountUpdateConflict", base: uiV12AccountConflictJSON()},
		{name: "role-grant-request", schema: uiV12IdentitySchema + "#/$defs/roleGrantRequest", base: uiV12RoleGrantJSON()},
		{name: "role-grant-view", schema: uiV12IdentitySchema + "#/$defs/roleGrantView", base: `{
			"bindingId": "` + uiV12UUID + `", "userId": "` + uiV12OtherUUID + `", "tenantId": "tenant-a",
			"role": "operator", "scope": {"clusterUids": ["cluster-uid-1"], "namespaces": ["payments"]},
			"authorizationVersion": 3, "grantedAt": "` + uiV12Timestamp + `", "revokedAt": null}`},
		{name: "subject-mapping-request", schema: uiV12IdentitySchema + "#/$defs/subjectMappingRequest", base: uiV12SubjectMappingJSON()},
		{name: "subject-mapping-view", schema: uiV12IdentitySchema + "#/$defs/subjectMappingView", base: uiV12SubjectMappingViewJSON()},
		{name: "session-view", schema: uiV12IdentitySchema + "#/$defs/sessionView", base: uiV12SessionViewJSON()},
		{name: "session-revocation-request", schema: uiV12IdentitySchema + "#/$defs/sessionRevocationRequest", base: `{
			"expectedSessionVersion": 7, "reason": "sign out everywhere"}`},
		{name: "account-view", schema: uiV12IdentitySchema + "#/$defs/accountView", base: uiV12AccountViewJSON()},
		{name: "admin-password-reset-request", schema: uiV12IdentitySchema + "#/$defs/adminPasswordResetRequest", base: uiV12AdminPasswordResetRequestJSON()},
		{name: "admin-password-reset-result", schema: uiV12IdentitySchema + "#/$defs/adminPasswordResetResult", base: uiV12AdminPasswordResetResultJSON()},

		{name: "step-up-command-challenge", schema: uiV12StepUpSchema + "#/$defs/challengeCreateRequest", base: uiV12CommandChallengeJSON()},
		{name: "step-up-user-administration-challenge", schema: uiV12StepUpSchema + "#/$defs/challengeCreateRequest", base: uiV12UserChallengeJSON()},
		{name: "step-up-mfa-enrollment-challenge", schema: uiV12StepUpSchema + "#/$defs/challengeCreateRequest", base: uiV12MFAChallengeJSON()},
		{name: "step-up-source-challenge", schema: uiV12StepUpSchema + "#/$defs/challengeCreateRequest", base: uiV12SourceChallengeJSON()},
		{name: "step-up-cluster-challenge", schema: uiV12StepUpSchema + "#/$defs/challengeCreateRequest", base: uiV12ClusterChallengeJSON()},
		{name: "step-up-registry-challenge", schema: uiV12StepUpSchema + "#/$defs/challengeCreateRequest", base: uiV12RegistryChallengeJSON()},
		{name: "step-up-execution-profile-challenge", schema: uiV12StepUpSchema + "#/$defs/challengeCreateRequest", base: uiV12ExecutionProfileChallengeJSON()},
		{name: "step-up-challenge-view", schema: uiV12StepUpSchema + "#/$defs/challengeView", base: uiV12ChallengeViewJSON()},
		{name: "step-up-verify-request", schema: uiV12StepUpSchema + "#/$defs/challengeVerifyRequest", base: `{
			"method": "totp", "credential": "123456", "expectedChallengeRevision": 1}`},
		{name: "step-up-session-view", schema: uiV12StepUpSchema + "#/$defs/stepUpSessionView", base: uiV12StepUpSessionJSON()},
		{name: "step-up-verify-partial", schema: uiV12StepUpSchema + "#/$defs/challengeVerifyResult", base: `{
			"verified": false, "method": "password", "challengeState": "pending",
			"completedMethods": ["password"], "acr": "urn:ops:loa:1", "stepUpSession": null,
			"verifiedAt": null, "failureCode": null}`},
		{name: "step-up-verify-rejected-first-factor", schema: uiV12StepUpSchema + "#/$defs/challengeVerifyResult", base: `{
			"verified": false, "method": "totp", "challengeState": "pending",
			"completedMethods": [], "acr": "urn:ops:loa:1", "stepUpSession": null,
			"verifiedAt": null, "failureCode": "invalid_credential"}`},
		{name: "step-up-verify-complete", schema: uiV12StepUpSchema + "#/$defs/challengeVerifyResult", base: uiV12VerifyResultJSON()},
		{name: "step-up-write-recheck", schema: uiV12StepUpSchema + "#/$defs/writeRecheck", base: uiV12WriteRecheckJSON("user_administration", "null")},
		{name: "step-up-write-recheck-command", schema: uiV12StepUpSchema + "#/$defs/writeRecheck", base: uiV12WriteRecheckJSON("command_execution", `"`+uiV12Digest+`"`)},
		{name: "mfa-enrollment-create-request", schema: uiV12StepUpSchema + "#/$defs/mfaEnrollmentCreateRequest", base: `{
			"method": "totp", "expectedCredentialVersion": 3}`},
		{name: "mfa-enrollment-created", schema: uiV12StepUpSchema + "#/$defs/mfaEnrollmentCreated", base: uiV12EnrollmentCreatedJSON()},
		{name: "mfa-enrollment-status-view", schema: uiV12StepUpSchema + "#/$defs/mfaEnrollmentView", base: uiV12EnrollmentViewJSON()},
		{name: "mfa-enrollment-verify-request", schema: uiV12StepUpSchema + "#/$defs/mfaEnrollmentVerifyRequest", base: `{
			"totpCode": "123456", "expectedEnrollmentRevision": 1,
			"stepUp": {"challengeId": "` + uiV12ThirdUUID + `", "sessionRef": "step-up-1"}}`},
		{name: "mfa-enrollment-verify-result", schema: uiV12StepUpSchema + "#/$defs/mfaEnrollmentVerifyResult", base: uiV12EnrollmentResultJSON()},
		{name: "recovery-code-verify-request", schema: uiV12StepUpSchema + "#/$defs/recoveryCodeVerifyRequest", base: `{
			"challengeId": "` + uiV12ThirdUUID + `", "recoveryCode": "ABCD-EFGH",
			"expectedChallengeRevision": 1}`},
		{name: "recovery-code-verify-result", schema: uiV12StepUpSchema + "#/$defs/recoveryCodeVerifyResult", base: uiV12RecoveryResultJSON()},
		{name: "admin-mfa-reset-request", schema: uiV12StepUpSchema + "#/$defs/adminMfaResetRequest", base: uiV12AdminResetRequestJSON()},
		{name: "admin-mfa-reset-result", schema: uiV12StepUpSchema + "#/$defs/adminMfaResetResult", base: uiV12AdminResetResultJSON()},

		{name: "observability-create-request", schema: uiV12ObservabilitySchema + "#/$defs/createRequest", base: uiV12QueryCreateJSON()},
		{name: "observability-create-request-cluster-scoped", schema: uiV12ObservabilitySchema + "#/$defs/createRequest", base: uiV12ClusterScopedCreateJSON()},
		{name: "observability-query-view", schema: uiV12ObservabilitySchema + "#/$defs/queryView", base: uiV12QueryViewJSON()},
		{name: "observability-result-view", schema: uiV12ObservabilitySchema + "#/$defs/resultView", base: uiV12ResultViewJSON()},
		{name: "observability-metric-result-view", schema: uiV12ObservabilitySchema + "#/$defs/resultView", base: uiV12MetricResultJSON()},
		{name: "observability-cancel-request", schema: uiV12ObservabilitySchema + "#/$defs/cancelRequest", base: `{
			"expectedRevision": 3, "reason": "navigated away"}`},
		{name: "observability-cancel-result", schema: uiV12ObservabilitySchema + "#/$defs/cancelResult", base: `{
			"queryId": "` + uiV12UUID + `", "state": "cancel_requested",
			"cancelRequestedAt": "` + uiV12LaterStamp + `", "termination": null}`},

		{name: "model-connection-upsert", schema: uiV12ModelSchema + "#/$defs/connectionUpsertRequest", base: uiV12ConnectionUpsertJSON()},
		{name: "model-connection-upsert-keep-credential", schema: uiV12ModelSchema + "#/$defs/connectionUpsertRequest", base: uiV12ConnectionKeepJSON()},
		{name: "model-connection-view", schema: uiV12ModelSchema + "#/$defs/connectionView", base: uiV12ConnectionViewJSON()},
		{name: "model-connection-test-request", schema: uiV12ModelSchema + "#/$defs/connectionTestRequest", base: `{
			"expectedRevision": 1}`},
		{name: "model-connection-test-result", schema: uiV12ModelSchema + "#/$defs/connectionTestResult", base: uiV12TestResultJSON()},
		{name: "model-capability-record", schema: uiV12ModelSchema + "#/$defs/capabilityRecord", base: uiV12CapabilityJSON()},
		{name: "model-run-option", schema: uiV12ModelSchema + "#/$defs/runOptionView", base: uiV12RunOptionJSON()},
		{name: "model-assistant-job-binding", schema: uiV12ModelSchema + "#/$defs/assistantJobBinding", base: uiV12JobBindingJSON()},
	}
}

func uiV12RejectionCases() []uiV12Case {
	return []uiV12Case{
		// Native identity: the client never supplies tenant, roles, administrator counts or delivery channels.
		{name: "login-request-self-granted-tenant", schema: uiV12IdentitySchema + "#/$defs/loginRequest", base: uiV12LoginJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["tenantId"] = "tenant-b"
		}},
		{name: "login-request-self-granted-roles", schema: uiV12IdentitySchema + "#/$defs/loginRequest", base: uiV12LoginJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["roles"] = []string{"platform_admin"}
		}},
		{name: "account-create-self-granted-tenant", schema: uiV12IdentitySchema + "#/$defs/accountCreateRequest", base: uiV12AccountCreateJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["tenantId"] = "tenant-b"
		}},
		{name: "account-create-self-granted-roles", schema: uiV12IdentitySchema + "#/$defs/accountCreateRequest", base: uiV12AccountCreateJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["roles"] = []string{"operator", "platform_admin"}
		}},
		{name: "account-create-email-setup-link", schema: uiV12IdentitySchema + "#/$defs/accountCreateRequest", base: uiV12AccountCreateJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["initialPasswordMode"] = "one_time_setup_link"
		}},
		{name: "account-create-without-initial-password", schema: uiV12IdentitySchema + "#/$defs/accountCreateRequest", base: uiV12AccountCreateJSON(), mutate: func(t *testing.T, m map[string]any) {
			delete(m, "initialPassword")
		}},
		{name: "account-update-client-supplied-administrator-state", schema: uiV12IdentitySchema + "#/$defs/accountUpdateRequest", base: uiV12AccountUpdateStatusJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["lastAdministratorCheck"] = map[string]any{"activeAdministrators": 1, "targetIsLastActive": true}
		}},
		{name: "account-update-empty-intent", schema: uiV12IdentitySchema + "#/$defs/accountUpdateRequest", base: uiV12AccountUpdateDisplayJSON(), mutate: func(t *testing.T, m map[string]any) {
			delete(m, "displayName")
		}},
		{name: "account-update-conflict-allows-disable", schema: uiV12IdentitySchema + "#/$defs/accountUpdateConflict", base: uiV12AccountConflictJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["effectiveStatus"] = "disabled"
		}},
		{name: "account-update-conflict-zero-administrators", schema: uiV12IdentitySchema + "#/$defs/accountUpdateConflict", base: uiV12AccountConflictJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["activeAdministrators"] = 0
		}},
		{name: "role-grant-combined-roles-field", schema: uiV12IdentitySchema + "#/$defs/roleGrantRequest", base: uiV12RoleGrantJSON(), mutate: func(t *testing.T, m map[string]any) {
			delete(m, "role")
			m["roles"] = []string{"operator", "platform_admin"}
		}},
		{name: "role-grant-combined-role-value", schema: uiV12IdentitySchema + "#/$defs/roleGrantRequest", base: uiV12RoleGrantJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["role"] = "operator,platform_admin"
		}},
		{name: "subject-mapping-by-login-name", schema: uiV12IdentitySchema + "#/$defs/subjectMappingRequest", base: uiV12SubjectMappingJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["matchBy"] = "loginName"
		}},
		{name: "subject-mapping-without-evidence", schema: uiV12IdentitySchema + "#/$defs/subjectMappingRequest", base: uiV12SubjectMappingJSON(), mutate: func(t *testing.T, m map[string]any) {
			delete(m, "evidenceDigest")
		}},
		{name: "subject-mapping-view-carries-roles", schema: uiV12IdentitySchema + "#/$defs/subjectMappingView", base: uiV12SubjectMappingViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["role"] = "platform_admin"
		}},
		{name: "session-loa2-from-password-only", schema: uiV12IdentitySchema + "#/$defs/sessionView", base: uiV12SessionViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["authMethods"] = []string{"password"}
		}},
		{name: "session-first-password-with-business-grants", schema: uiV12IdentitySchema + "#/$defs/sessionView", base: uiV12SessionViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["mode"] = "first_password_only"
			m["mustChangePassword"] = true
		}},
		{name: "session-csrf-weakened-samesite", schema: uiV12IdentitySchema + "#/$defs/sessionView", base: uiV12SessionViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			nested(t, m, "csrf")["sameSite"] = "Lax"
		}},
		{name: "session-zero-credential-version", schema: uiV12IdentitySchema + "#/$defs/sessionView", base: uiV12SessionViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			nested(t, m, "revocation")["credentialVersion"] = 0
		}},
		{name: "account-view-zero-active-administrators", schema: uiV12IdentitySchema + "#/$defs/accountView", base: uiV12AccountViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			nested(t, m, "administratorState")["activeAdministrators"] = 0
		}},
		{name: "admin-password-reset-client-forced-flag", schema: uiV12IdentitySchema + "#/$defs/adminPasswordResetRequest", base: uiV12AdminPasswordResetRequestJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["mustChangePassword"] = true
		}},
		{name: "admin-password-reset-result-zero-credential-version", schema: uiV12IdentitySchema + "#/$defs/adminPasswordResetResult", base: uiV12AdminPasswordResetResultJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["credentialVersion"] = 0
		}},

		// Step-up and MFA: the server owns the method policy, accumulated state and write-time recheck.
		{name: "step-up-verify-client-assurance-level", schema: uiV12StepUpSchema + "#/$defs/challengeVerifyRequest", base: uiV12VerifyRequestJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["assuranceLevel"] = "urn:ops:loa:2"
		}},
		{name: "step-up-verify-client-acr", schema: uiV12StepUpSchema + "#/$defs/challengeVerifyRequest", base: uiV12VerifyRequestJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["acr"] = "urn:ops:loa:2"
		}},
		{name: "step-up-create-client-downscales-methods", schema: uiV12StepUpSchema + "#/$defs/challengeCreateRequest", base: uiV12UserChallengeJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["requestedMethods"] = []string{"password"}
		}},
		{name: "step-up-command-operation-without-command-binding", schema: uiV12StepUpSchema + "#/$defs/challengeCreateRequest", base: uiV12CommandChallengeJSON(), mutate: func(t *testing.T, m map[string]any) {
			delete(m, "command")
		}},
		{name: "step-up-user-operation-with-command-binding", schema: uiV12StepUpSchema + "#/$defs/challengeCreateRequest", base: uiV12UserChallengeJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["command"] = uiV12Object(t, uiV12CommandBindingJSON())
		}},
		{name: "step-up-mfa-enrollment-target-kind-mismatch", schema: uiV12StepUpSchema + "#/$defs/challengeCreateRequest", base: uiV12MFAChallengeJSON(), mutate: func(t *testing.T, m map[string]any) {
			nested(t, m, "target")["kind"] = "user"
			delete(nested(t, m, "target"), "enrollmentId")
			nested(t, m, "target")["userId"] = uiV12UUID
		}},
		{name: "step-up-source-operation-with-user-target", schema: uiV12StepUpSchema + "#/$defs/challengeCreateRequest", base: uiV12SourceChallengeJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["target"] = map[string]any{"kind": "user", "userId": uiV12UUID, "revision": 2}
		}},
		{name: "step-up-verified-single-totp", schema: uiV12StepUpSchema + "#/$defs/challengeVerifyResult", base: uiV12VerifyResultJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["completedMethods"] = []string{"totp"}
		}},
		{name: "step-up-verified-without-session", schema: uiV12StepUpSchema + "#/$defs/challengeVerifyResult", base: uiV12VerifyResultJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["stepUpSession"] = nil
		}},
		{name: "step-up-verify-result-claims-write-recheck", schema: uiV12StepUpSchema + "#/$defs/challengeVerifyResult", base: uiV12VerifyResultJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["writeRecheck"] = uiV12Object(t, uiV12WriteRecheckJSON("user_administration", "null"))
		}},
		{name: "step-up-challenge-verified-partial-methods", schema: uiV12StepUpSchema + "#/$defs/challengeView", base: uiV12ChallengeViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["state"] = "verified"
			m["verifiedAt"] = uiV12LaterStamp
		}},
		{name: "step-up-pending-with-both-methods", schema: uiV12StepUpSchema + "#/$defs/challengeView", base: uiV12ChallengeViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["completedMethods"] = []string{"password", "totp"}
		}},
		{name: "step-up-used-challenge-with-attempts", schema: uiV12StepUpSchema + "#/$defs/challengeView", base: uiV12ChallengeViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["state"] = "used"
			m["remainingAttempts"] = 2
		}},
		{name: "mfa-status-view-repeats-provisioning-uri", schema: uiV12StepUpSchema + "#/$defs/mfaEnrollmentView", base: uiV12EnrollmentViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["otpauthUri"] = "otpauth://totp/Ops:alice?secret=ABC&issuer=Ops"
		}},
		{name: "mfa-enrollment-verify-result-without-codes", schema: uiV12StepUpSchema + "#/$defs/mfaEnrollmentVerifyResult", base: uiV12EnrollmentResultJSON(), mutate: func(t *testing.T, m map[string]any) {
			delete(m, "recoveryCodes")
		}},
		{name: "mfa-enrollment-verify-short-code", schema: uiV12StepUpSchema + "#/$defs/mfaEnrollmentVerifyRequest", base: `{
			"totpCode": "123456", "expectedEnrollmentRevision": 1,
			"stepUp": {"challengeId": "` + uiV12ThirdUUID + `", "sessionRef": "step-up-1"}}`, mutate: func(t *testing.T, m map[string]any) {
			m["totpCode"] = "12345"
		}},
		{name: "mfa-enrollment-verify-without-step-up", schema: uiV12StepUpSchema + "#/$defs/mfaEnrollmentVerifyRequest", base: `{
			"totpCode": "123456", "expectedEnrollmentRevision": 1,
			"stepUp": {"challengeId": "` + uiV12ThirdUUID + `", "sessionRef": "step-up-1"}}`, mutate: func(t *testing.T, m map[string]any) {
			delete(m, "stepUp")
		}},
		{name: "recovery-code-without-challenge-id", schema: uiV12StepUpSchema + "#/$defs/recoveryCodeVerifyRequest", base: `{
			"challengeId": "` + uiV12ThirdUUID + `", "recoveryCode": "ABCD-EFGH",
			"expectedChallengeRevision": 1}`, mutate: func(t *testing.T, m map[string]any) {
			delete(m, "challengeId")
		}},
		{name: "mfa-enrollment-verify-short-recovery-set", schema: uiV12StepUpSchema + "#/$defs/mfaEnrollmentVerifyResult", base: uiV12EnrollmentResultJSON(), mutate: func(t *testing.T, m map[string]any) {
			nested(t, m, "recoveryCodes")["codes"] = []string{"ABCD-EFGH"}
		}},
		{name: "recovery-code-without-session-revocation", schema: uiV12StepUpSchema + "#/$defs/recoveryCodeVerifyResult", base: uiV12RecoveryResultJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["sessionsRevoked"] = 0
		}},
		{name: "recovery-code-escalates-to-step-up", schema: uiV12StepUpSchema + "#/$defs/recoveryCodeVerifyResult", base: uiV12RecoveryResultJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["stepUpSession"] = map[string]any{"sessionRef": "step-up-1"}
		}},
		{name: "admin-mfa-reset-result-without-recheck", schema: uiV12StepUpSchema + "#/$defs/adminMfaResetResult", base: uiV12AdminResetResultJSON(), mutate: func(t *testing.T, m map[string]any) {
			delete(m, "writeRecheck")
		}},
		{name: "admin-mfa-reset-result-without-revocation", schema: uiV12StepUpSchema + "#/$defs/adminMfaResetResult", base: uiV12AdminResetResultJSON(), mutate: func(t *testing.T, m map[string]any) {
			delete(m, "revocation")
		}},
		{name: "write-recheck-command-without-digest", schema: uiV12StepUpSchema + "#/$defs/writeRecheck", base: uiV12WriteRecheckJSON("command_execution", `"`+uiV12Digest+`"`), mutate: func(t *testing.T, m map[string]any) {
			m["actualCommandDigest"] = nil
		}},
		{name: "write-recheck-without-write-attempt", schema: uiV12StepUpSchema + "#/$defs/writeRecheck", base: uiV12WriteRecheckJSON("user_administration", "null"), mutate: func(t *testing.T, m map[string]any) {
			delete(m, "writeAttemptId")
		}},

		// Independent observation: no AI session, fixed identity, samples never imply a total.
		{name: "observation-create-binds-ai-conversation", schema: uiV12ObservabilitySchema + "#/$defs/createRequest", base: uiV12QueryCreateJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["conversationId"] = uiV12UUID
		}},
		{name: "observation-create-free-form-query", schema: uiV12ObservabilitySchema + "#/$defs/createRequest", base: uiV12QueryCreateJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["query"] = "level:error | count()"
		}},
		{name: "observation-create-zero-credential-revision", schema: uiV12ObservabilitySchema + "#/$defs/createRequest", base: uiV12QueryCreateJSON(), mutate: func(t *testing.T, m map[string]any) {
			nested(t, m, "source")["credentialRevision"] = 0
		}},
		{name: "observation-create-non-half-open-window", schema: uiV12ObservabilitySchema + "#/$defs/createRequest", base: uiV12QueryCreateJSON(), mutate: func(t *testing.T, m map[string]any) {
			nested(t, m, "window")["bounds"] = "[from,to]"
		}},
		{name: "observation-create-without-namespace-uid", schema: uiV12ObservabilitySchema + "#/$defs/createRequest", base: uiV12QueryCreateJSON(), mutate: func(t *testing.T, m map[string]any) {
			delete(nested(t, nested(t, m, "resource"), "namespaceScope"), "namespaceUid")
		}},
		{name: "observation-create-forged-cluster-namespace-uid", schema: uiV12ObservabilitySchema + "#/$defs/createRequest", base: uiV12ClusterScopedCreateJSON(), mutate: func(t *testing.T, m map[string]any) {
			nested(t, nested(t, m, "resource"), "namespaceScope")["namespaceUid"] = "ns-uid-1"
		}},
		{name: "observation-result-total-from-samples", schema: uiV12ObservabilitySchema + "#/$defs/resultView", base: uiV12ResultViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			population := nested(t, m, "population")
			population["basis"] = "not_measured"
			population["method"] = nil
			population["count"] = 42
		}},
		{name: "observation-result-sample-inferred-population", schema: uiV12ObservabilitySchema + "#/$defs/resultView", base: uiV12ResultViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			nested(t, m, "samples")["inferredPopulation"] = 42
		}},
		{name: "observation-result-representative-sample", schema: uiV12ObservabilitySchema + "#/$defs/resultView", base: uiV12ResultViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			nested(t, m, "samples")["representative"] = true
		}},
		{name: "observation-result-truncated-without-reason", schema: uiV12ObservabilitySchema + "#/$defs/resultView", base: uiV12ResultViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			truncation := nested(t, m, "truncation")
			truncation["truncated"] = true
			truncation["reasons"] = []string{}
		}},
		{name: "observation-result-complete-without-measured-population", schema: uiV12ObservabilitySchema + "#/$defs/resultView", base: uiV12ResultViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			population := nested(t, m, "population")
			population["basis"] = "not_measured"
			population["method"] = nil
			delete(population, "count")
		}},
		{name: "observation-result-no-data-with-samples", schema: uiV12ObservabilitySchema + "#/$defs/resultView", base: uiV12ResultViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["status"] = "no_data"
			m["exact"] = false
			population := nested(t, m, "population")
			population["count"] = 0
		}},
		{name: "observation-result-metric-with-log-population", schema: uiV12ObservabilitySchema + "#/$defs/resultView", base: uiV12MetricResultJSON(), mutate: func(t *testing.T, m map[string]any) {
			nested(t, m, "population")["basis"] = "exact"
			nested(t, m, "population")["count"] = 3
		}},
		{name: "observation-view-completed-without-result", schema: uiV12ObservabilitySchema + "#/$defs/queryView", base: uiV12QueryViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["resultRef"] = nil
		}},
		{name: "observation-view-terminated-without-receipt", schema: uiV12ObservabilitySchema + "#/$defs/queryView", base: uiV12QueryViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["state"] = "terminated"
			m["cancelRequestedAt"] = uiV12LaterStamp
			m["termination"] = nil
		}},
		{name: "observation-view-cancel-requested-already-terminated", schema: uiV12ObservabilitySchema + "#/$defs/queryView", base: uiV12QueryViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["state"] = "cancel_requested"
			m["cancelRequestedAt"] = uiV12LaterStamp
			m["termination"] = uiV12Termination(t)
		}},
		{name: "observation-view-running-with-cancel-flag", schema: uiV12ObservabilitySchema + "#/$defs/queryView", base: uiV12QueryViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["state"] = "running"
			m["cancelRequestedAt"] = uiV12LaterStamp
		}},
		{name: "observation-view-running-with-result", schema: uiV12ObservabilitySchema + "#/$defs/queryView", base: uiV12QueryViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["state"] = "running"
		}},
		{name: "observation-view-terminated-with-result", schema: uiV12ObservabilitySchema + "#/$defs/queryView", base: uiV12QueryViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["state"] = "terminated"
			m["cancelRequestedAt"] = uiV12LaterStamp
			m["termination"] = uiV12Termination(t)
		}},
		{name: "observation-view-expired-with-result", schema: uiV12ObservabilitySchema + "#/$defs/queryView", base: uiV12QueryViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["state"] = "expired"
		}},
		{name: "observation-reauthorization-allowed-with-denial", schema: uiV12ObservabilitySchema + "#/$defs/queryView", base: uiV12QueryViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			nested(t, m, "reauthorization")["denialCode"] = "authorization_revoked"
		}},
		{name: "observation-coverage-unknown-but-complete", schema: uiV12ObservabilitySchema + "#/$defs/queryView", base: uiV12QueryViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			coverage := nested(t, m, "coverage")
			coverage["basis"] = "unknown"
			coverage["complete"] = true
		}},

		// Model: four user fields only, credential never echoed, unknown admission stays closed.
		{name: "model-connection-fifth-field", schema: uiV12ModelSchema + "#/$defs/connectionUpsertRequest", base: uiV12ConnectionUpsertJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["temperature"] = 0.2
		}},
		{name: "model-connection-view-echoes-api-key", schema: uiV12ModelSchema + "#/$defs/connectionView", base: uiV12ConnectionViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["apiKey"] = "sk-test-0123456789"
		}},
		{name: "model-credential-echoes-secret", schema: uiV12ModelSchema + "#/$defs/connectionView", base: uiV12ConnectionViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			nested(t, m, "credential")["secretValue"] = "sk-test-0123456789"
		}},
		{name: "model-admission-without-evidence", schema: uiV12ModelSchema + "#/$defs/connectionView", base: uiV12ConnectionViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			admission := nested(t, m, "admission")
			admission["state"] = "admitted"
			admission["profileReference"] = uiV12ProfileRef
			admission["evaluatedAt"] = uiV12Timestamp
		}},
		{name: "model-unknown-admission-with-profile", schema: uiV12ModelSchema + "#/$defs/connectionView", base: uiV12ConnectionViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			nested(t, m, "admission")["profileReference"] = uiV12ProfileRef
		}},
		{name: "model-context-window-too-small", schema: uiV12ModelSchema + "#/$defs/connectionUpsertRequest", base: uiV12ConnectionUpsertJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["contextWindow"] = 512
		}},
		{name: "model-connection-window-client-claims-source", schema: uiV12ModelSchema + "#/$defs/connectionUpsertRequest", base: uiV12ConnectionUpsertJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["contextWindow"] = map[string]any{"value": 32768, "source": "provider_reported", "providerReportedMax": 131072}
		}},
		{name: "model-credential-replace-without-secret", schema: uiV12ModelSchema + "#/$defs/connectionUpsertRequest", base: uiV12ConnectionUpsertJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["credential"] = map[string]any{"action": "replace"}
		}},
		{name: "model-credential-keep-with-secret", schema: uiV12ModelSchema + "#/$defs/connectionUpsertRequest", base: uiV12ConnectionUpsertJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["credential"] = map[string]any{"action": "keep", "apiKey": "sk-test-0123456789"}
		}},
		{name: "model-provider-window-without-measurement", schema: uiV12ModelSchema + "#/$defs/connectionView", base: uiV12ConnectionViewJSON(), mutate: func(t *testing.T, m map[string]any) {
			window := nested(t, m, "contextWindow")
			window["source"] = "provider_reported"
			window["providerReportedMax"] = nil
		}},
		{name: "model-test-claims-quality-verdict", schema: uiV12ModelSchema + "#/$defs/connectionTestResult", base: uiV12TestResultJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["qualityAssessed"] = true
		}},
		{name: "model-test-carries-admission-transition", schema: uiV12ModelSchema + "#/$defs/connectionTestResult", base: uiV12TestResultJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["admission"] = map[string]any{"state": "admitted", "evidenceDigest": uiV12Digest}
		}},
		{name: "model-quality-assessed-without-evidence", schema: uiV12ModelSchema + "#/$defs/capabilityRecord", base: uiV12CapabilityJSON(), mutate: func(t *testing.T, m map[string]any) {
			nested(t, m, "quality")["state"] = "assessed"
		}},
		{name: "model-run-option-not-ready-worker", schema: uiV12ModelSchema + "#/$defs/runOptionView", base: uiV12RunOptionJSON(), mutate: func(t *testing.T, m map[string]any) {
			nested(t, m, "worker")["state"] = "stale"
		}},
		{name: "model-run-option-not-admitted", schema: uiV12ModelSchema + "#/$defs/runOptionView", base: uiV12RunOptionJSON(), mutate: func(t *testing.T, m map[string]any) {
			nested(t, m, "admission")["state"] = "pending"
		}},
		{name: "model-job-old-contract-version", schema: uiV12ModelSchema + "#/$defs/assistantJobBinding", base: uiV12JobBindingJSON(), mutate: func(t *testing.T, m map[string]any) {
			m["jobContractVersion"] = "investigation-job/v3"
		}},
		{name: "model-job-without-admission-evidence", schema: uiV12ModelSchema + "#/$defs/assistantJobBinding", base: uiV12JobBindingJSON(), mutate: func(t *testing.T, m map[string]any) {
			delete(nested(t, m, "profile"), "admissionEvidenceDigest")
		}},
		{name: "model-job-without-worker-capability", schema: uiV12ModelSchema + "#/$defs/assistantJobBinding", base: uiV12JobBindingJSON(), mutate: func(t *testing.T, m map[string]any) {
			delete(m, "workerCapability")
		}},
	}
}

func uiV12LoginJSON() string {
	return `{"loginName": "alice", "password": "CorrectHorseBattery1"}`
}

func uiV12AccountCreateJSON() string {
	return `{"loginName": "bob", "displayName": "Bob", "initialPassword": "InitialPassword12"}`
}

func uiV12AccountUpdateDisplayJSON() string {
	return `{"displayName": "Bob", "expectedRevision": 2}`
}

func uiV12AccountUpdateStatusJSON() string {
	return `{"status": "disabled", "expectedRevision": 2}`
}

func uiV12AccountConflictJSON() string {
	return `{"code": "last_active_administrator", "activeAdministrators": 1,
		"protectedUserId": "` + uiV12UUID + `", "attemptedStatus": "disabled",
		"effectiveStatus": "active", "detectedAt": "` + uiV12LaterStamp + `"}`
}

func uiV12AccountViewJSON() string {
	return `{"userId": "` + uiV12UUID + `", "loginName": "alice", "displayName": "Alice", "status": "active",
		"mustChangePassword": false, "subject": "principal-7f3a", "revision": 4,
		"revocation": {"credentialVersion": 3, "sessionVersion": 7, "authorizationVersion": 5},
		"roleGrants": [{"bindingId": "` + uiV12OtherUUID + `", "userId": "` + uiV12UUID + `", "tenantId": "tenant-a",
			"role": "platform_admin", "scope": {"clusterUids": [], "namespaces": []},
			"authorizationVersion": 5, "grantedAt": "` + uiV12Timestamp + `", "revokedAt": null}],
		"administratorState": {"activeAdministrators": 2, "isAdministrator": true,
			"isLastActiveAdministrator": false}}`
}

func uiV12RoleGrantJSON() string {
	return `{"userId": "` + uiV12UUID + `", "role": "operator",
		"scope": {"clusterUids": ["cluster-uid-1"], "namespaces": ["payments"]},
		"expectedAuthorizationVersion": 3, "reason": "on-call rotation"}`
}

func uiV12SubjectMappingJSON() string {
	return `{"legacySubject": "oidc|old-123", "userId": "` + uiV12UUID + `", "basis": "verified_inventory",
		"evidenceDigest": "` + uiV12Digest + `", "expectedUserRevision": 2}`
}

func uiV12SubjectMappingViewJSON() string {
	return `{"mappingId": "` + uiV12UUID + `", "legacySubject": "oidc|old-123", "userId": "` + uiV12OtherUUID + `",
		"tenantId": "tenant-a", "basis": "verified_inventory", "evidenceDigest": "` + uiV12Digest + `",
		"mappedAt": "` + uiV12Timestamp + `"}`
}

func uiV12SessionViewJSON() string {
	return `{"sessionRef": "sess-opaque-01", "subject": "principal-7f3a", "tenantId": "tenant-a", "mode": "full",
		"authMethods": ["password", "totp"], "acr": "urn:ops:loa:2",
		"issuedAt": "` + uiV12Timestamp + `", "absoluteExpiresAt": "` + uiV12SessionExpiry + `",
		"idleExpiresAt": "2026-10-21T09:30:00Z", "mustChangePassword": false,
		"revocation": {"credentialVersion": 3, "sessionVersion": 7, "authorizationVersion": 5},
		"csrf": {"headerName": "X-CSRF-Token", "token": "csrf-token-0123456789",
			"cookieName": "__Host-ops-session", "sameSite": "Strict"},
		"authorization": {"grants": [
			{"bindingId": "` + uiV12UUID + `", "userId": "` + uiV12OtherUUID + `", "tenantId": "tenant-a",
			 "role": "operator", "scope": {"clusterUids": ["cluster-uid-1"], "namespaces": ["payments"]},
			 "authorizationVersion": 5, "grantedAt": "` + uiV12Timestamp + `", "revokedAt": null}]},
		"administratorState": {"activeAdministrators": 2, "isAdministrator": false, "isLastActiveAdministrator": false}}`
}

func uiV12CommandBindingJSON() string {
	return `{"assessmentId": "` + uiV12ThirdUUID + `", "riskAssessmentVersion": "risk/v1",
		"executionRequestDigest": "` + uiV12Digest + `", "policyVersion": "policy/v1",
		"incidentId": "` + uiV12OtherUUID + `", "executionProfileId": "` + uiV12UUID + `",
		"executionProfileVersion": 3, "actualCommandDigest": "` + uiV12Digest + `"}`
}

func uiV12K8sTargetJSON() string {
	return `{"kind": "k8s_object", "canonicalId": "` + uiV12CanonicalID + `",
		"uid": "pod-uid-1", "clusterUid": "cluster-uid-1", "namespace": "payments", "revision": 4}`
}

func uiV12SourceTargetJSON() string {
	return `{"kind": "source_registration", "registrationId": "` + uiV12UUID + `", "revision": 4}`
}

func uiV12ClusterTargetJSON() string {
	return `{"kind": "cluster_registration", "clusterId": "` + uiV12UUID + `", "revision": 2}`
}

func uiV12RegistryTargetJSON() string {
	return `{"kind": "registry_object", "objectKind": "version", "objectId": "` + uiV12UUID + `", "revision": 3}`
}

func uiV12ProfileTargetJSON() string {
	return `{"kind": "execution_profile", "executionProfileId": "` + uiV12UUID + `", "revision": 5}`
}

func uiV12CommandChallengeJSON() string {
	return `{"operation": "command_execution", "target": ` + uiV12K8sTargetJSON() + `,
		"command": ` + uiV12CommandBindingJSON() + `, "expectedSessionVersion": 7}`
}

func uiV12UserChallengeJSON() string {
	return `{"operation": "user_administration", "target": {"kind": "user",
		"userId": "` + uiV12UUID + `", "revision": 2}, "expectedSessionVersion": 7}`
}

func uiV12MFAChallengeJSON() string {
	return `{"operation": "mfa_enrollment", "target": {"kind": "mfa_enrollment",
		"enrollmentId": "` + uiV12ThirdUUID + `", "revision": 1}, "expectedSessionVersion": 7}`
}

func uiV12SourceChallengeJSON() string {
	return `{"operation": "source_administration", "target": ` + uiV12SourceTargetJSON() + `,
		"expectedSessionVersion": 7}`
}

func uiV12ClusterChallengeJSON() string {
	return `{"operation": "cluster_administration", "target": ` + uiV12ClusterTargetJSON() + `,
		"expectedSessionVersion": 7}`
}

func uiV12RegistryChallengeJSON() string {
	return `{"operation": "registry_administration", "target": ` + uiV12RegistryTargetJSON() + `,
		"expectedSessionVersion": 7}`
}

func uiV12ExecutionProfileChallengeJSON() string {
	return `{"operation": "execution_profile_administration", "target": ` + uiV12ProfileTargetJSON() + `,
		"expectedSessionVersion": 7}`
}

func uiV12ChallengeViewJSON() string {
	return `{"challengeId": "` + uiV12UUID + `", "operation": "command_execution",
		"target": ` + uiV12K8sTargetJSON() + `, "bindingDigest": "` + uiV12Digest + `",
		"requiredMethods": ["password", "totp"], "completedMethods": ["password"],
		"state": "pending", "createdAt": "` + uiV12Timestamp + `", "expiresAt": "` + uiV12LaterStamp + `",
		"verifiedAt": null, "attemptLimit": 5, "remainingAttempts": 4}`
}

func uiV12VerifyRequestJSON() string {
	return `{"method": "totp", "credential": "123456", "expectedChallengeRevision": 1}`
}

func uiV12StepUpSessionJSON() string {
	return `{"sessionRef": "step-up-1", "operation": "command_execution",
		"bindingDigest": "` + uiV12Digest + `", "verifiedMethods": ["password", "totp"],
		"absoluteExpiresAt": "` + uiV12SessionExpiry + `", "idleExpiresAt": "` + uiV12LaterStamp + `"}`
}

func uiV12VerifyResultJSON() string {
	return `{"verified": true, "method": "totp", "challengeState": "verified",
		"completedMethods": ["password", "totp"], "acr": "urn:ops:loa:2",
		"stepUpSession": ` + uiV12StepUpSessionJSON() + `,
		"verifiedAt": "` + uiV12LaterStamp + `", "failureCode": null}`
}

func uiV12WriteRecheckJSON(operation, actualCommandDigest string) string {
	return `{"writeAttemptId": "` + uiV12ThirdUUID + `", "writeOperation": "` + operation + `",
		"checkedAt": "` + uiV12LaterStamp + `", "result": "passed",
		"sessionDigest": "` + uiV12Digest + `", "authorizationVersion": 5, "credentialVersion": 3,
		"objectDigest": "` + uiV12OtherDigest + `", "actualCommandDigest": ` + actualCommandDigest + `}`
}

func uiV12EnrollmentCreatedJSON() string {
	return `{"enrollmentId": "` + uiV12UUID + `", "method": "totp", "state": "pending_confirmation",
		"otpauthUri": "otpauth://totp/Ops:alice?secret=ABC&issuer=Ops",
		"createdAt": "` + uiV12Timestamp + `", "expiresAt": "` + uiV12LaterStamp + `"}`
}

func uiV12EnrollmentViewJSON() string {
	return `{"enrollmentId": "` + uiV12UUID + `", "method": "totp", "state": "confirmed",
		"createdAt": "` + uiV12Timestamp + `", "expiresAt": "` + uiV12LaterStamp + `",
		"confirmedAt": "` + uiV12LaterStamp + `"}`
}

func uiV12EnrollmentResultJSON() string {
	return `{"enrollmentId": "` + uiV12UUID + `", "state": "confirmed", "confirmedAt": "` + uiV12LaterStamp + `",
		"credentialVersion": 4, "recoveryCodes": {"codes": ["AAAA-1111", "BBBB-2222", "CCCC-3333", "DDDD-4444",
		"EEEE-5555", "FFFF-6666", "GGGG-7777", "HHHH-8888", "IIII-9999", "JJJJ-0000"]}}`
}

func uiV12RecoveryResultJSON() string {
	return `{"verified": true, "method": "recovery_code", "acr": "urn:ops:loa:1", "sessionsRevoked": 3,
		"recoveryCodesRemaining": 9, "mustReenrollMfa": true, "stepUpSession": null}`
}

func uiV12AdminResetRequestJSON() string {
	return `{"userId": "` + uiV12UUID + `", "confirmLoginName": "alice", "reason": "lost device",
		"expectedUserRevision": 2, "expectedCredentialVersion": 4}`
}

func uiV12AdminResetResultJSON() string {
	return `{"userId": "` + uiV12UUID + `", "mfaState": "reset_required", "credentialVersion": 5,
		"sessionsRevoked": 2, "revocation": {"credentialVersion": 5, "sessionVersion": 8, "authorizationVersion": 5},
		"writeRecheck": ` + uiV12WriteRecheckJSON("mfa_reset", "null") + `,
		"auditRecordId": "` + uiV12OtherUUID + `"}`
}

func uiV12AdminPasswordResetRequestJSON() string {
	return `{"initialPassword": "InitialPassword12", "expectedRevision": 2,
		"expectedCredentialVersion": 4}`
}

func uiV12AdminPasswordResetResultJSON() string {
	return `{"userId": "` + uiV12UUID + `", "passwordState": "change_required", "credentialVersion": 5,
		"sessionsRevoked": 0, "revocation": {"credentialVersion": 5, "sessionVersion": 8, "authorizationVersion": 5},
		"auditRecordId": "` + uiV12OtherUUID + `"}`
}

func uiV12ResourceJSON() string {
	return `{"canonicalId": "` + uiV12CanonicalID + `", "uid": "pod-uid-1",
		"clusterUid": "cluster-uid-1",
		"namespaceScope": {"kind": "namespaced", "namespace": "payments", "namespaceUid": "ns-uid-1"}}`
}

func uiV12SourceJSON() string {
	return `{"registrationId": "` + uiV12UUID + `", "registrationRevision": 4, "credentialRevision": 2,
		"qualificationDigest": "` + uiV12Digest + `", "collectorVersion": "victorialogs/1.2.3"}`
}

func uiV12WindowJSON() string {
	return `{"from": "` + uiV12Timestamp + `", "to": "` + uiV12LaterStamp + `",
		"timezone": "Asia/Shanghai", "bounds": "[from,to)"}`
}

func uiV12CoverageJSON() string {
	return `{"coveredFrom": "` + uiV12Timestamp + `", "coveredTo": "` + uiV12LaterStamp + `",
		"complete": true, "basis": "source_proof", "gaps": []}`
}

func uiV12ReauthorizationJSON() string {
	return `{"checkedAt": "` + uiV12LaterStamp + `", "authorizationVersion": 5,
		"sourceRegistrationRevision": 4, "result": "allowed", "denialCode": null}`
}

func uiV12QueryCreateJSON() string {
	return `{"schemaVersion": "ui-v12-observability-query/v1", "queryKind": "logs",
		"templateId": "logs.by-workload", "templateVersion": "logs/v1", "resource": ` + uiV12ResourceJSON() + `,
		"source": ` + uiV12SourceJSON() + `, "window": ` + uiV12WindowJSON() + `,
		"filters": [{"field": "level", "operator": "eq", "value": "error"}],
		"budget": {"maxRows": 500, "maxBytes": 1048576, "maxDurationSeconds": 10, "maxSourceCalls": 2}}`
}

func uiV12ClusterScopedCreateJSON() string {
	return `{"schemaVersion": "ui-v12-observability-query/v1", "queryKind": "metrics",
		"templateId": "metrics.node-ready", "templateVersion": "metrics/v1",
		"resource": {"canonicalId": "k8s+v1://tenant-a/cluster-1/core/Node/node-uid-1",
			"uid": "node-uid-1", "clusterUid": "cluster-uid-1",
			"namespaceScope": {"kind": "cluster_scoped", "namespace": null, "namespaceUid": null}},
		"source": ` + uiV12SourceJSON() + `, "window": ` + uiV12WindowJSON() + `,
		"budget": {"maxRows": 500, "maxBytes": 1048576, "maxDurationSeconds": 10, "maxSourceCalls": 2}}`
}

func uiV12QueryViewJSON() string {
	return `{"queryId": "` + uiV12UUID + `", "schemaVersion": "ui-v12-observability-query/v1",
		"state": "completed", "ownerSubject": "principal-7f3a", "tenantId": "tenant-a",
		"resource": ` + uiV12ResourceJSON() + `, "source": ` + uiV12SourceJSON() + `,
		"window": ` + uiV12WindowJSON() + `, "coverage": ` + uiV12CoverageJSON() + `,
		"createdAt": "` + uiV12Timestamp + `", "updatedAt": "` + uiV12LaterStamp + `",
		"expiresAt": "2026-10-22T09:00:00Z",
		"resultRef": {"resultId": "` + uiV12OtherUUID + `", "digest": "` + uiV12Digest + `",
			"expiresAt": "2026-10-22T09:00:00Z"},
		"cancelRequestedAt": null, "termination": null, "failure": null,
		"reauthorization": ` + uiV12ReauthorizationJSON() + `}`
}

func uiV12ResultViewJSON() string {
	return `{"schemaVersion": "ui-v12-observability-result/v1", "status": "complete",
		"resultKind": "log_statistics", "exact": true,
		"resultId": "` + uiV12OtherUUID + `",
		"queryId": "` + uiV12UUID + `", "computedAt": "` + uiV12LaterStamp + `",
		"expiresAt": "2026-10-22T09:00:00Z", "resource": ` + uiV12ResourceJSON() + `,
		"source": ` + uiV12SourceJSON() + `, "window": ` + uiV12WindowJSON() + `,
		"coverage": ` + uiV12CoverageJSON() + `,
		"population": {"basis": "exact", "method": "source_total", "count": 128},
		"samples": {"returned": 1, "inferredPopulation": null,
			"items": [{"timestamp": "` + uiV12Timestamp + `", "level": "error",
			"message": "connection refused", "fields": {"pod": "api-1"}}]},
		"statistics": [{"key": "log.error_count", "value": 12, "unit": "count", "basis": "exact"}],
		"truncation": {"truncated": false, "reasons": []},
		"warnings": ["bounded_sanitized_samples_not_total_count"],
		"resultDigest": "` + uiV12Digest + `", "reauthorization": ` + uiV12ReauthorizationJSON() + `}`
}

func uiV12MetricResultJSON() string {
	return `{"schemaVersion": "ui-v12-observability-result/v1", "status": "complete",
		"resultKind": "metric_series", "exact": true, "resultId": "` + uiV12OtherUUID + `",
		"queryId": "` + uiV12UUID + `", "computedAt": "` + uiV12LaterStamp + `",
		"expiresAt": "2026-10-22T09:00:00Z", "resource": ` + uiV12ResourceJSON() + `,
		"source": ` + uiV12SourceJSON() + `, "window": ` + uiV12WindowJSON() + `,
		"coverage": ` + uiV12CoverageJSON() + `,
		"population": {"basis": "not_applicable", "method": null},
		"samples": {"returned": 0, "inferredPopulation": null, "items": []},
		"statistics": [{"key": "node.cpu.utilization", "value": 0.42, "unit": "ratio", "basis": "exact"}],
		"truncation": {"truncated": false, "reasons": []},
		"warnings": [],
		"resultDigest": "` + uiV12Digest + `", "reauthorization": ` + uiV12ReauthorizationJSON() + `}`
}

func uiV12ConnectionUpsertJSON() string {
	return `{"baseURL": "https://llm.internal.example/v1", "modelID": "qwen2.5-72b-instruct",
		"contextWindow": 32768,
		"credential": {"action": "replace", "apiKey": "sk-test-0123456789"},
		"expectedRevision": null}`
}

func uiV12ConnectionKeepJSON() string {
	return `{"baseURL": "https://llm.internal.example/v1", "modelID": "qwen2.5-72b-instruct",
		"contextWindow": null, "credential": {"action": "keep"}, "expectedRevision": 1}`
}

func uiV12ConnectionViewJSON() string {
	return `{"connectionId": "` + uiV12UUID + `", "revision": 1,
		"baseURL": "https://llm.internal.example/v1", "modelID": "qwen2.5-72b-instruct",
		"contextWindow": {"value": 32768, "source": "administrator_entered",
			"providerReportedMax": 131072},
		"credential": {"store": "openbao", "vaultPathRef": "secret://model/connections/conn-1",
			"configured": true},
		"connection": {"state": "untested", "checkedAt": null, "failureCode": null},
		"admission": {"state": "unknown", "evidenceDigest": null, "profileReference": null, "evaluatedAt": null},
		"updatedAt": "` + uiV12Timestamp + `"}`
}

func uiV12TestResultJSON() string {
	return `{"testId": "` + uiV12UUID + `", "connectionId": "` + uiV12OtherUUID + `",
		"connectionState": "passed", "protocol": {"detected": "openai_compatible", "taskProtocolSupported": true},
		"checks": [{"name": "reachability", "state": "passed", "detail": null},
			{"name": "authentication", "state": "passed", "detail": null},
			{"name": "model_presence", "state": "passed", "detail": null}],
		"testedAt": "` + uiV12LaterStamp + `"}`
}

func uiV12CapabilityJSON() string {
	return `{"connection": {"state": "passed", "checkedAt": "` + uiV12LaterStamp + `", "failureCode": null},
		"admission": {"state": "admitted", "evidenceDigest": "` + uiV12Digest + `",
			"profileReference": "` + uiV12ProfileRef + `", "evaluatedAt": "` + uiV12Timestamp + `"},
		"task": {"state": "supported", "taskKinds": ["data_query"]},
		"quality": {"state": "not_assessed", "rubricVersion": null, "evidenceDigest": null}}`
}

func uiV12RunOptionJSON() string {
	return `{"optionId": "` + uiV12UUID + `", "modelID": "qwen2.5-72b-instruct", "contextWindow": 32768,
		"profileReference": "` + uiV12ProfileRef + `",
		"admission": {"state": "admitted", "evidenceDigest": "` + uiV12Digest + `"},
		"worker": {"state": "ready", "catalogVersion": "assistant-tools/v2", "readyUntil": "` + uiV12LaterStamp + `"},
		"taskKinds": ["data_query"]}`
}

func uiV12JobBindingJSON() string {
	return `{"jobContractVersion": "ui-v12-assistant-job/v1", "jobId": "` + uiV12UUID + `",
		"taskKind": "data_query",
		"profile": {"reference": "` + uiV12ProfileRef + `", "modelID": "qwen2.5-72b-instruct",
			"contextWindow": 32768, "admissionEvidenceDigest": "` + uiV12Digest + `"},
		"workerCapability": {"workerId": "sp06-dispatcher:http://ops-investigator.ops.svc.cluster.local:8080",
			"catalogVersion": "assistant-tools/v2", "modelProfileVersion": "` + uiV12ProfileRef + `",
			"readyAt": "` + uiV12Timestamp + `", "expiresAt": "` + uiV12LaterStamp + `"},
		"frozenAt": "` + uiV12Timestamp + `"}`
}

func uiV12Termination(t *testing.T) map[string]any {
	t.Helper()
	return uiV12Object(t, `{"requestedAt": "`+uiV12LaterStamp+`", "deliveredAt": "2026-10-21T10:00:05Z",
		"sourceStopped": true, "receiptDigest": "`+uiV12Digest+`"}`)
}

func uiV12Inventory(t *testing.T) map[string]map[string]bool {
	t.Helper()
	var record struct {
		Schemas []struct {
			ID          string   `json:"id"`
			Definitions []string `json:"definitions"`
		} `json:"schemas"`
	}
	raw := uiV12Evidence(t, "p0-schema-inventory.json")
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatalf("decode schema inventory: %v", err)
	}
	out := make(map[string]map[string]bool, len(record.Schemas))
	for _, schema := range record.Schemas {
		names := make(map[string]bool, len(schema.Definitions))
		for _, name := range schema.Definitions {
			names[name] = true
		}
		out[schema.ID] = names
	}
	return out
}

func uiV12Rejections(t *testing.T) map[string]bool {
	t.Helper()
	var record struct {
		Cases []struct {
			Name string `json:"name"`
		} `json:"cases"`
	}
	raw := uiV12Evidence(t, "p0-schema-rejections.json")
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatalf("decode rejection evidence: %v", err)
	}
	out := make(map[string]bool, len(record.Cases))
	for _, test := range record.Cases {
		out[test.Name] = true
	}
	return out
}

func uiV12Evidence(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "docs", "plans", "ui-v12-replication-20261021", "evidence", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read evidence %s: %v", path, err)
	}
	return raw
}

func uiV12Object(t *testing.T, raw string) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return value
}

func nested(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := m[key].(map[string]any)
	if !ok {
		t.Fatalf("payload has no object %q", key)
	}
	return value
}

func sameStringSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	keys := make([]string, 0, len(a))
	for key := range a {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !b[key] {
			return false
		}
	}
	return true
}
