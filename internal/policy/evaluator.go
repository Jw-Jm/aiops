package policy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/open-policy-agent/opa/v1/rego"
	"ops-platform/internal/auth"
	"ops-platform/internal/configregistry"
)

type RequestType string

const (
	RequestTool   RequestType = "tool"
	RequestAction RequestType = "action"
)

type PrincipalType string

const (
	PrincipalOperator      PrincipalType = "operator"
	PrincipalPlatformAdmin PrincipalType = "platform_admin"
	PrincipalAgent         PrincipalType = "agent"
)

type Risk string

const (
	RiskLow    Risk = "low"
	RiskMedium Risk = "medium"
	RiskHigh   Risk = "high"
)

var (
	ErrPolicyDenied      = errors.New("policy evaluation denied the request")
	ErrPolicyEvaluation  = errors.New("policy evaluation failed")
	ErrPolicyInput       = errors.New("policy evaluation input is invalid")
	commandDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// InvocationContext is populated by the authenticated internal service boundary,
// never by an HTTP request body.
type InvocationContext struct {
	Verified     bool
	TenantID     uuid.UUID
	Scope        configregistry.Scope
	AllowedTools []string
}

// PolicyInput contains authenticated identity and server-resolved operation facts.
// Actual command text and credentials are deliberately excluded; only command
// digests cross this boundary. AllowedTools comes from the trusted tool registry
// and must never be populated from request bodies.
type PolicyInput struct {
	Request          auth.RequestContext
	PrincipalType    PrincipalType
	RequestType      RequestType
	Scope            configregistry.Scope
	Invocation       *InvocationContext
	ToolName         string
	AllowedTools     []string
	ToolReadOnly     bool
	Risk             Risk
	RiskAcknowledged bool
	ClusterLevel     bool
	RootLevel        bool
	// StepUpVerified is set only after validating and touching the bound session
	// with auth.ValidateStepUpForContext in the request transaction.
	StepUpVerified bool
	// These contain SHA-256 digests only; plaintext command bodies are not policy input.
	ActualCommandDigest    string
	ConfirmedCommandDigest string
}

type PolicyDecision struct {
	Allow          bool     `json:"allow"`
	Risk           Risk     `json:"risk"`
	Reasons        []string `json:"reasons"`
	RequiresStepUp bool     `json:"requiresStepUp"`
	PolicyVersion  string   `json:"policyVersion"`
	DecisionID     string   `json:"decisionId"`
}

type ActivePolicyResolver interface {
	ResolveActive(context.Context, uuid.UUID, configregistry.Kind, string, configregistry.Scope, time.Time) (configregistry.PublishedVersion, error)
}

type PolicyEvaluator struct {
	resolver    ActivePolicyResolver
	compiler    *BundleCompiler
	logicalName string
	now         func() time.Time
}

func NewEvaluator(resolver ActivePolicyResolver, compiler *BundleCompiler, logicalName string) (*PolicyEvaluator, error) {
	if resolver == nil || compiler == nil || strings.TrimSpace(logicalName) == "" {
		return nil, errors.New("active policy resolver, bundle compiler, and policy name are required")
	}
	return &PolicyEvaluator{resolver: resolver, compiler: compiler, logicalName: logicalName, now: time.Now}, nil
}

func (evaluator *PolicyEvaluator) Evaluate(ctx context.Context, input PolicyInput) (PolicyDecision, error) {
	if err := ctx.Err(); err != nil {
		return denyDecision(input, "", "evaluation_cancelled"), fmt.Errorf("%w: %v", ErrPolicyEvaluation, err)
	}
	if input.Request.TenantID == uuid.Nil || input.Scope.Validate() != nil ||
		(input.RequestType != RequestTool && input.RequestType != RequestAction) ||
		(input.Risk != "" && input.Risk != RiskLow && input.Risk != RiskMedium && input.Risk != RiskHigh) {
		return denyDecision(input, "", "invalid_policy_input"), ErrPolicyInput
	}
	version, err := evaluator.resolver.ResolveActive(ctx, input.Request.TenantID, configregistry.KindPolicy,
		evaluator.logicalName, input.Scope, evaluator.now().UTC())
	if err != nil {
		return denyDecision(input, "", "active_policy_unavailable"), fmt.Errorf("%w: resolve active policy: %v", ErrPolicyEvaluation, err)
	}
	if version.TenantID != input.Request.TenantID || version.Kind != configregistry.KindPolicy || version.LogicalName != evaluator.logicalName {
		return denyDecision(input, version.VersionID.String(), "policy_scope_mismatch"), ErrPolicyDenied
	}
	var contentMetadata policyBundle
	if err := json.Unmarshal(version.Content, &contentMetadata); err != nil || contentMetadata.Name != version.LogicalName {
		return denyDecision(input, version.VersionID.String(), "policy_identity_mismatch"), ErrPolicyDenied
	}
	prepared, err := evaluator.compiler.Prepare(ctx, version)
	if err != nil {
		return denyDecision(input, version.VersionID.String(), "policy_bundle_untrusted_or_invalid"), fmt.Errorf("%w: %v", ErrPolicyEvaluation, err)
	}

	principalTrusted := trustedPrincipal(input)
	tenantMatched := principalTenant(input) == version.TenantID && input.Request.TenantID == version.TenantID
	scopeAllowed := principalTrusted && scopeAllowed(input)
	toolAllowed := false
	if input.RequestType == RequestTool && input.ToolName != "" {
		toolAllowed = contains(input.AllowedTools, input.ToolName)
		if input.PrincipalType == PrincipalAgent && input.Invocation != nil {
			toolAllowed = contains(input.Invocation.AllowedTools, input.ToolName)
		}
	}
	commandMatched := commandDigestsMatch(input.ActualCommandDigest, input.ConfirmedCommandDigest)
	actionAllowed := principalTrusted && input.PrincipalType == PrincipalOperator
	risk := input.Risk
	if risk == "" {
		risk = RiskLow
	}
	requiresStepUp := input.ClusterLevel || input.RootLevel
	regoInput := map[string]any{
		"requestType": string(input.RequestType), "principalType": string(input.PrincipalType),
		"tenantMatched": tenantMatched, "scopeAllowed": scopeAllowed,
		"toolAllowed": toolAllowed, "toolReadOnly": input.ToolReadOnly,
		"actionAllowed": actionAllowed,
		"risk":          string(risk), "riskAcknowledged": risk == RiskLow || input.RiskAcknowledged,
		"clusterLevel": input.ClusterLevel, "rootLevel": input.RootLevel,
		"requiresStepUp": requiresStepUp, "stepUpVerified": input.StepUpVerified,
		"commandDigestMatched": commandMatched,
	}
	query := prepared.actionQuery
	if input.RequestType == RequestTool {
		query = prepared.toolQuery
	}
	results, err := query.Eval(ctx, rego.EvalInput(regoInput))
	if err != nil {
		return denyDecision(input, version.VersionID.String(), "policy_evaluation_error"), ErrPolicyEvaluation
	}
	if len(results) != 1 {
		return denyDecision(input, version.VersionID.String(), "policy_default_deny"), ErrPolicyDenied
	}
	value, ok := results[0].Bindings["x"]
	if !ok {
		return denyDecision(input, version.VersionID.String(), "policy_result_missing"), ErrPolicyDenied
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return denyDecision(input, version.VersionID.String(), "policy_result_invalid"), fmt.Errorf("%w: encode policy result: %v", ErrPolicyEvaluation, err)
	}
	var decision PolicyDecision
	if err := json.Unmarshal(encoded, &decision); err != nil || !validDecision(decision) {
		return denyDecision(input, version.VersionID.String(), "policy_result_invalid"), ErrPolicyDenied
	}
	decision.PolicyVersion = version.VersionID.String()
	decision.Risk = maxRisk(risk, decision.Risk)
	decision.RequiresStepUp = decision.RequiresStepUp || requiresStepUp
	hardDenied := false
	if decision.RequiresStepUp && !input.StepUpVerified {
		decision.Allow = false
		if !contains(decision.Reasons, "step_up_required") {
			decision.Reasons = append(decision.Reasons, "step_up_required")
		}
	}
	if !tenantMatched || !scopeAllowed || !principalTrusted ||
		(input.RequestType == RequestTool && (!input.ToolReadOnly || !toolAllowed)) ||
		(input.RequestType == RequestAction && (input.PrincipalType == PrincipalAgent || !actionAllowed || !commandMatched || (risk != RiskLow && !input.RiskAcknowledged))) {
		decision.Allow = false
		hardDenied = true
		if !tenantMatched {
			decision.Reasons = append(decision.Reasons, "tenant_scope_mismatch")
		}
		if !scopeAllowed {
			decision.Reasons = append(decision.Reasons, "authorization_scope_mismatch")
		}
		if input.RequestType == RequestAction && !commandMatched {
			decision.Reasons = append(decision.Reasons, "command_digest_mismatch")
		}
	}
	decision.DecisionID = decisionID(version.Digest, input)
	if !decision.Allow {
		if input.RequestType == RequestAction && decision.RequiresStepUp && !input.StepUpVerified && !hardDenied &&
			len(decision.Reasons) == 1 && decision.Reasons[0] == "step_up_required" {
			return decision, nil
		}
		return decision, ErrPolicyDenied
	}
	return decision, nil
}

func trustedPrincipal(input PolicyInput) bool {
	switch input.PrincipalType {
	case PrincipalOperator:
		return hasRole(input.Request.Roles, auth.Operator) && input.Request.TenantID != uuid.Nil && input.Request.Subject != ""
	case PrincipalPlatformAdmin:
		return hasRole(input.Request.Roles, auth.PlatformAdmin) && input.Request.TenantID != uuid.Nil && input.Request.Subject != ""
	case PrincipalAgent:
		return input.RequestType == RequestTool && input.Invocation != nil && input.Invocation.Verified &&
			input.Invocation.TenantID != uuid.Nil && input.Invocation.Scope.Validate() == nil &&
			input.Invocation.TenantID == input.Request.TenantID && sameScope(input.Invocation.Scope, input.Scope)
	default:
		return false
	}
}

func principalTenant(input PolicyInput) uuid.UUID {
	if input.PrincipalType == PrincipalAgent && input.Invocation != nil {
		return input.Invocation.TenantID
	}
	return input.Request.TenantID
}

func scopeAllowed(input PolicyInput) bool {
	if input.PrincipalType == PrincipalAgent {
		return input.Invocation != nil && input.Invocation.Verified &&
			input.Invocation.TenantID == input.Request.TenantID && sameScope(input.Invocation.Scope, input.Scope)
	}
	switch input.Scope.Type {
	case configregistry.ScopeTenant:
		return input.PrincipalType == PrincipalPlatformAdmin
	case configregistry.ScopeCluster:
		return containsUUID(input.Request.ClusterScopes, input.Scope.ClusterID)
	case configregistry.ScopeNamespace:
		if !containsUUID(input.Request.ClusterScopes, input.Scope.ClusterID) {
			return false
		}
		for _, scope := range input.Request.NamespaceScopes {
			if scope.ClusterID == input.Scope.ClusterID && scope.Namespace == input.Scope.Namespace {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func commandDigestsMatch(actual, confirmed string) bool {
	return commandDigestPattern.MatchString(actual) && commandDigestPattern.MatchString(confirmed) && actual == confirmed
}

func validDecision(decision PolicyDecision) bool {
	if decision.Risk != RiskLow && decision.Risk != RiskMedium && decision.Risk != RiskHigh {
		return false
	}
	for _, reason := range decision.Reasons {
		if strings.TrimSpace(reason) == "" || len(reason) > 128 {
			return false
		}
	}
	return true
}

func denyDecision(input PolicyInput, version, reason string) PolicyDecision {
	decision := PolicyDecision{Risk: RiskHigh, Reasons: []string{reason}, RequiresStepUp: input.ClusterLevel || input.RootLevel, PolicyVersion: version}
	decision.DecisionID = decisionID("unavailable", input)
	return decision
}

func decisionID(digest string, input PolicyInput) string {
	type identity struct {
		BundleDigest         string               `json:"bundleDigest"`
		TenantID             string               `json:"tenantId"`
		Principal            PrincipalType        `json:"principal"`
		RequestType          RequestType          `json:"requestType"`
		Scope                configregistry.Scope `json:"scope"`
		ToolName             string               `json:"toolName,omitempty"`
		AllowedTools         []string             `json:"allowedTools,omitempty"`
		Risk                 Risk                 `json:"risk"`
		RiskAcknowledged     bool                 `json:"riskAcknowledged"`
		ToolReadOnly         bool                 `json:"toolReadOnly"`
		ClusterLevel         bool                 `json:"clusterLevel"`
		RootLevel            bool                 `json:"rootLevel"`
		Command              string               `json:"commandDigest,omitempty"`
		Confirmed            string               `json:"confirmedCommandDigest,omitempty"`
		StepUp               bool                 `json:"stepUpVerified"`
		PrincipalTrusted     bool                 `json:"principalTrusted"`
		TenantMatched        bool                 `json:"tenantMatched"`
		ScopeAllowed         bool                 `json:"scopeAllowed"`
		ToolAllowed          bool                 `json:"toolAllowed"`
		ActionAllowed        bool                 `json:"actionAllowed"`
		CommandDigestMatched bool                 `json:"commandDigestMatched"`
	}
	allowedTools := input.AllowedTools
	if input.PrincipalType == PrincipalAgent && input.Invocation != nil {
		allowedTools = input.Invocation.AllowedTools
	}
	allowedTools = append([]string(nil), allowedTools...)
	sort.Strings(allowedTools)
	risk := input.Risk
	if risk == "" {
		risk = RiskLow
	}
	canonical := identity{BundleDigest: digest, TenantID: input.Request.TenantID.String(), Principal: input.PrincipalType,
		RequestType: input.RequestType, Scope: input.Scope, ToolName: input.ToolName, AllowedTools: allowedTools,
		Risk: risk, RiskAcknowledged: input.RiskAcknowledged, ToolReadOnly: input.ToolReadOnly,
		ClusterLevel: input.ClusterLevel, RootLevel: input.RootLevel, Command: input.ActualCommandDigest,
		Confirmed: input.ConfirmedCommandDigest, StepUp: input.StepUpVerified,
		PrincipalTrusted: trustedPrincipal(input), TenantMatched: principalTenant(input) == input.Request.TenantID,
		ScopeAllowed: scopeAllowed(input), ToolAllowed: input.ToolName != "" && contains(allowedTools, input.ToolName),
		CommandDigestMatched: commandDigestsMatch(input.ActualCommandDigest, input.ConfirmedCommandDigest)}
	encoded, _ := json.Marshal(canonical)
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func maxRisk(first, second Risk) Risk {
	if riskRank(second) > riskRank(first) {
		return second
	}
	return first
}

func riskRank(risk Risk) int {
	switch risk {
	case RiskHigh:
		return 3
	case RiskMedium:
		return 2
	default:
		return 1
	}
}

func hasRole(roles []auth.Role, wanted auth.Role) bool {
	for _, role := range roles {
		if role == wanted {
			return true
		}
	}
	return false
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func containsUUID(values []uuid.UUID, wanted uuid.UUID) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func sameScope(first, second configregistry.Scope) bool {
	return first.Type == second.Type && first.ClusterID == second.ClusterID && first.Namespace == second.Namespace
}
