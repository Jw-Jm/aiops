package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/rego"
	"ops-platform/internal/configregistry"
)

var (
	ErrPolicyBundleSignature = errors.New("policy bundle signature or digest is invalid")
	ErrPolicyBundleInvalid   = errors.New("policy bundle is invalid")
)

type policyBundle struct {
	SchemaVersion string `json:"schemaVersion"`
	Name          string `json:"name"`
	Modules       []struct {
		ID      string `json:"id"`
		Content string `json:"content"`
	} `json:"modules"`
}

type preparedBundle struct {
	toolQuery   rego.PreparedEvalQuery
	actionQuery rego.PreparedEvalQuery
}

// BundleCompiler verifies signatures before cache lookup or compilation. Cached
// prepared queries are keyed by the canonical content digest and safe for reuse.
type BundleCompiler struct {
	verifier configregistry.SignatureVerifier
	mu       sync.RWMutex
	cache    map[string]*preparedBundle
}

func NewBundleCompiler(verifier configregistry.SignatureVerifier) (*BundleCompiler, error) {
	if verifier == nil {
		return nil, errors.New("policy bundle signature verifier is required")
	}
	return &BundleCompiler{verifier: verifier, cache: make(map[string]*preparedBundle)}, nil
}

// ValidatePolicyPublication is called only after the registry has verified the
// publication signature. It compiles and checks both fail-closed entrypoints
// before the version can be committed.
func (compiler *BundleCompiler) ValidatePolicyPublication(ctx context.Context, content json.RawMessage) error {
	if err := configregistry.ValidateContent(configregistry.KindPolicy, content); err != nil {
		return fmt.Errorf("%w: %v", ErrPolicyBundleInvalid, err)
	}
	var bundle policyBundle
	if err := json.Unmarshal(content, &bundle); err != nil {
		return fmt.Errorf("%w: decode policy bundle: %v", ErrPolicyBundleInvalid, err)
	}
	_, err := compiler.compile(ctx, bundle)
	return err
}

func (compiler *BundleCompiler) Prepare(ctx context.Context, version configregistry.PublishedVersion) (*preparedBundle, error) {
	if version.Kind != configregistry.KindPolicy || version.TenantID == uuid.Nil || version.LogicalName == "" {
		return nil, ErrPolicyBundleInvalid
	}
	message, digest, err := configregistry.SigningPayload(version.TenantID, version.Kind, version.LogicalName, version.Content)
	if err != nil || digest != version.Digest {
		return nil, ErrPolicyBundleSignature
	}
	if err := compiler.verifier.Verify(ctx, version.SignerKeyID, message, version.Signature); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPolicyBundleSignature, err)
	}
	if err := configregistry.ValidateContent(configregistry.KindPolicy, version.Content); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPolicyBundleInvalid, err)
	}

	compiler.mu.RLock()
	prepared := compiler.cache[digest]
	compiler.mu.RUnlock()
	if prepared != nil {
		return prepared, nil
	}
	var bundle policyBundle
	if err := json.Unmarshal(version.Content, &bundle); err != nil {
		return nil, fmt.Errorf("%w: decode policy bundle: %v", ErrPolicyBundleInvalid, err)
	}
	compiled, err := compiler.compile(ctx, bundle)
	if err != nil {
		return nil, err
	}
	compiler.mu.Lock()
	if existing := compiler.cache[digest]; existing != nil {
		compiled = existing
	} else {
		compiler.cache[digest] = compiled
	}
	compiler.mu.Unlock()
	return compiled, nil
}

func (compiler *BundleCompiler) compile(ctx context.Context, bundle policyBundle) (*preparedBundle, error) {
	if bundle.SchemaVersion != "policy-registry/v1" || bundle.Name == "" || len(bundle.Modules) == 0 {
		return nil, ErrPolicyBundleInvalid
	}
	modules := make([]func(*rego.Rego), 0, len(bundle.Modules))
	seen := make(map[string]struct{}, len(bundle.Modules))
	for _, module := range bundle.Modules {
		if strings.TrimSpace(module.ID) == "" || strings.TrimSpace(module.Content) == "" {
			return nil, ErrPolicyBundleInvalid
		}
		if _, exists := seen[module.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate module id", ErrPolicyBundleInvalid)
		}
		seen[module.ID] = struct{}{}
		modules = append(modules, rego.Module(module.ID, module.Content))
	}
	prepare := func(query string) (rego.PreparedEvalQuery, error) {
		capabilities := ast.CapabilitiesForThisVersion()
		capabilities.AllowNet = []string{}
		builtins := make([]*ast.Builtin, 0, len(capabilities.Builtins))
		for _, builtin := range capabilities.Builtins {
			if !builtin.IsNondeterministic() {
				builtins = append(builtins, builtin)
			}
		}
		capabilities.Builtins = builtins
		options := []func(*rego.Rego){rego.Query(query), rego.StrictBuiltinErrors(true), rego.Capabilities(capabilities)}
		options = append(options, modules...)
		return rego.New(options...).PrepareForEval(ctx)
	}
	tool, err := prepare("x = data.ops.policy.tool_decision")
	if err != nil {
		return nil, fmt.Errorf("%w: compile tool policy", ErrPolicyBundleInvalid)
	}
	action, err := prepare("x = data.ops.policy.action_decision")
	if err != nil {
		return nil, fmt.Errorf("%w: compile action policy", ErrPolicyBundleInvalid)
	}
	prepared := &preparedBundle{toolQuery: tool, actionQuery: action}
	if err := validateDefaultDeny(ctx, "tool", prepared.toolQuery); err != nil {
		return nil, err
	}
	if err := validateDefaultDeny(ctx, "action", prepared.actionQuery); err != nil {
		return nil, err
	}
	return prepared, nil
}

func validateDefaultDeny(ctx context.Context, kind string, query rego.PreparedEvalQuery) error {
	results, err := query.Eval(ctx, rego.EvalInput(map[string]any{}))
	if err != nil {
		return fmt.Errorf("%w: evaluate default %s decision", ErrPolicyBundleInvalid, kind)
	}
	if len(results) != 1 {
		return fmt.Errorf("%w: %s decision must define a default result", ErrPolicyBundleInvalid, kind)
	}
	value, ok := results[0].Bindings["x"]
	if !ok {
		return fmt.Errorf("%w: %s decision result is missing", ErrPolicyBundleInvalid, kind)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("%w: encode default %s decision: %v", ErrPolicyBundleInvalid, kind, err)
	}
	var decision PolicyDecision
	if err := decodeDecision(encoded, &decision); err != nil || decision.Allow {
		return fmt.Errorf("%w: %s decision must default to a valid deny result", ErrPolicyBundleInvalid, kind)
	}
	return nil
}
