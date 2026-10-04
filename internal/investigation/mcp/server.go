// Package mcp serves the official MCP Go SDK Streamable HTTP transport.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"ops-platform/internal/auth"

	"ops-platform/internal/investigation"
	"ops-platform/internal/investigation/tools"
	"strings"
	"time"
)

type Gateway struct {
	Repository investigation.Repository
	Signer     investigation.ContextSigner
	Trust      auth.WorkloadTrust
	API        tools.SemanticAPI
	// Authorize invokes the existing signed OPA policy resolver using current
	// subject grants. Nil always denies; the model never supplies this decision.
	Authorize func(context.Context, investigation.Job, string, json.RawMessage) error
}
type verified struct {
	Claims investigation.InvocationClaims
	Job    investigation.Job
	Lease  investigation.Lease
}

func (g Gateway) Handler() (http.Handler, error) {
	defs, err := tools.Catalog()
	if err != nil {
		return nil, err
	}
	outputSchema, err := tools.OutputSchema()
	if err != nil {
		return nil, err
	}
	catalog := tools.Digest(defs)
	server := sdk.NewServer(&sdk.Implementation{Name: "ops-platform", Version: "sp06-v2"}, nil)
	// Official SDK middleware preserves JSON-RPC behavior while recording
	// authenticated attempts to invoke unregistered capabilities.
	server.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, request sdk.Request) (sdk.Result, error) {
			if method == "tools/call" {
				if call, ok := request.(*sdk.CallToolRequest); ok {
					known := false
					for _, d := range defs {
						if d.Name == call.Params.Name {
							known = true
							break
						}
					}
					if !known && call.Extra != nil && call.Extra.TokenInfo != nil {
						if v, ok := call.Extra.TokenInfo.Extra["opsVerified"].(verified); ok {
							_ = g.Repository.RecordDenial(ctx, v.Lease, "unregistered", "UNKNOWN_TOOL")
						}
					}
				}
			}
			return next(ctx, method, request)
		}
	})
	for _, d := range defs {
		server.AddTool(&sdk.Tool{Name: d.Name, Description: d.Schema["description"].(string), InputSchema: d.Schema, OutputSchema: outputSchema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, r *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			if r.Extra == nil || r.Extra.TokenInfo == nil {
				return failed(d.Name, investigation.ErrDenied), nil
			}
			v, ok := r.Extra.TokenInfo.Extra["opsVerified"].(verified)
			if !ok {
				return failed(d.Name, investigation.ErrDenied), nil
			}
			ctx = investigation.WithDataClasses(ctx, v.Claims.AllowedDataClasses)
			deny := func(err error) (*sdk.CallToolResult, error) {
				code := err.Error()
				if len(code) > 128 {
					code = "INVESTIGATION_NOT_AUTHORIZED"
				}
				_ = g.Repository.RecordDenial(ctx, v.Lease, d.Name, code)
				return g.failed(ctx, v, d.Name, errors.New(code)), nil
			}
			if d.Disabled != "" {
				return deny(errors.New("CAPABILITY_DISABLED: " + d.Disabled))
			}
			if err := v.Claims.Bind(v.Job, v.Lease, catalog, d.Name); err != nil {
				return deny(err)
			}
			if g.Authorize == nil {
				return deny(investigation.ErrDenied)
			}
			if err := g.Authorize(ctx, v.Job, d.Name, r.Params.Arguments); err != nil {
				return deny(investigation.ErrDenied)
			}
			if err := d.Validate(r.Params.Arguments); err != nil {
				return deny(investigation.ErrInvalid)
			}
			if g.API == nil {
				return deny(investigation.ErrDenied)
			}
			if err := g.API.CheckScope(ctx, v.Job, d.Name, r.Params.Arguments); err != nil {
				return deny(err)
			}
			var meta struct {
				StepID uuid.UUID `json:"ops/stepId"`
				Seq    *int64    `json:"ops/callSeq"`
				JTI    string    `json:"ops/jti"`
			}
			b, _ := json.Marshal(r.Params.GetMeta())
			if json.Unmarshal(b, &meta) != nil {
				return deny(investigation.ErrInvalid)
			}
			reserve := investigation.Usage{ToolCalls: 1, ResultBytes: 64 << 10, EvidenceItems: 100}
			if d.Name == "get_dependencies" || d.Name == "get_impact_scope" {
				reserve.GraphNodes = 200
			}
			if d.RawQuery {
				reserve.RawQueries = 1
			}
			c := investigation.Call{StepID: meta.StepID, ContextID: v.Claims.ContextID, Seq: meta.Seq, JTI: meta.JTI, Name: d.Name, ArgsDigest: investigation.ArgumentsDigest(r.Params.Arguments), Reserve: reserve, Claims: &v.Claims}
			if _, err := g.Repository.BeginCall(ctx, v.Lease, c); err != nil {
				return deny(err)
			}
			bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			out, err := g.API.Invoke(bounded, v.Job, d.Name, r.Params.Arguments)
			if err == nil && bounded.Err() != nil {
				err = bounded.Err()
			}
			if err != nil {
				code := "SOURCE_DEGRADED"
				if errors.Is(err, investigation.ErrDenied) {
					code = "INVESTIGATION_NOT_AUTHORIZED"
				}
				if errors.Is(err, context.DeadlineExceeded) {
					code = "SOURCE_TIMEOUT"
				}
				_ = g.Repository.FailStep(ctx, v.Lease, c.StepID, code)
				return g.failed(ctx, v, d.Name, errors.New(code)), nil
			}
			// Sanitization happens before either model-visible output or persistence.
			out, err = tools.Sanitize(out, int(reserve.ResultBytes))
			if err != nil {
				_ = g.Repository.FailStep(ctx, v.Lease, c.StepID, "RESULT_REJECTED")
				return g.failed(ctx, v, d.Name, err), nil
			}
			j, err := g.Repository.Get(ctx, v.Job.TenantID, v.Job.JobID)
			if err != nil {
				return g.failed(ctx, v, d.Name, err), nil
			}
			used := reserve
			used.EvidenceItems = int64(len(out.EvidenceRefs))
			used.ResultBytes = reserve.ResultBytes // output envelope also consumes bytes; conservatively bounded
			out.Budget.Consumed = j.Consumed.Add(used)
			out.Budget.Remaining = j.Budget.Usage.Sub(j.Consumed.Add(j.Reserved))
			result, _ := json.Marshal(out)
			if tools.ValidateOutput(result) != nil || len(result) > int(reserve.ResultBytes) {
				_ = g.Repository.FailStep(ctx, v.Lease, c.StepID, "RESULT_LIMIT")
				return g.failed(ctx, v, d.Name, investigation.ErrBudget), nil
			}
			result, err = g.Repository.CompleteToolStep(ctx, v.Lease, c.StepID, result, used)
			if err != nil {
				if errors.Is(err, investigation.ErrDenied) || errors.Is(err, investigation.ErrInvalid) {
					_ = g.Repository.FailStep(ctx, v.Lease, c.StepID, "RESULT_REJECTED")
				}
				return g.failed(ctx, v, d.Name, err), nil
			}
			if json.Unmarshal(result, &out) != nil {
				return g.failed(ctx, v, d.Name, investigation.ErrDenied), nil
			}
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(result)}}, StructuredContent: out, IsError: out.State != "succeeded"}, nil
		})
	}
	transport := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{JSONResponse: true, SessionTimeout: 2 * time.Minute})
	verifier := func(ctx context.Context, token string, r *http.Request) (*sdkauth.TokenInfo, error) {
		if r.Header.Get("Origin") != "" {
			return nil, sdkauth.ErrInvalidToken
		}
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			return nil, sdkauth.ErrInvalidToken
		}
		workload, err := auth.VerifyWorkload(auth.WithWorkloadTrust(ctx, g.Trust), r.TLS.PeerCertificates[0])
		if err != nil {
			return nil, sdkauth.ErrInvalidToken
		}
		claims, err := g.Signer.VerifyContext(ctx, token, "platform-mcp-gateway", workload.URI)
		if err != nil {
			return nil, sdkauth.ErrInvalidToken
		}
		j, l, err := g.Repository.Authenticate(ctx, claims, catalog)
		if err != nil {
			return nil, sdkauth.ErrInvalidToken
		}
		return &sdkauth.TokenInfo{Expiration: time.Unix(claims.ExpiresAt, 0), Extra: map[string]any{"opsVerified": verified{claims, j, l}}}, nil
	}
	return sdkauth.RequireBearerToken(verifier, nil)(transport), nil
}
func (g Gateway) failed(ctx context.Context, v verified, tool string, err error) *sdk.CallToolResult {
	j, e := g.Repository.Get(ctx, v.Job.TenantID, v.Job.JobID)
	if e != nil {
		return failed(tool, err)
	}
	return failed(tool, err, &j)
}
func failed(tool string, err error, jobs ...*investigation.Job) *sdk.CallToolResult {
	out := tools.Result{SchemaVersion: "tool-response/v2", Tool: tool, State: "failed", Data: map[string]any{}, EvidenceRefs: []string{}, Partial: true, DegradedSources: []string{}, ErrorCode: err.Error()}
	if len(jobs) > 0 {
		j := jobs[0]
		out.Budget.Consumed = j.Consumed
		out.Budget.Remaining = j.Budget.Usage.Sub(j.Consumed.Add(j.Reserved))
		out.Data = map[string]any{"budgetAvailable": true}
	} else {
		out.Data = map[string]any{"budgetAvailable": false}
	}
	if strings.HasPrefix(err.Error(), "CAPABILITY_DISABLED") {
		out.Data.(map[string]any)["capabilityState"] = "disabled"
		out.Data.(map[string]any)["verification"] = "unverified"
	}
	b, _ := json.Marshal(out)
	return &sdk.CallToolResult{IsError: true, StructuredContent: out, Content: []sdk.Content{&sdk.TextContent{Text: string(b)}}}
}
