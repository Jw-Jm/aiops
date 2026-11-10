package httpapi

import (
	"encoding/json"

	"github.com/google/uuid"
	"ops-platform/internal/investigation"
)

// publicInvestigationStep is the public projection of one committed step.
//
// The stored step keeps the whole tool result of the Worker: the assistant
// context tool returns the frozen model input, and the model step keeps the
// raw provider response. That material is internal execution context, not the
// public contract, so the public view publishes the documented envelope of the
// result and a bounded public subset of its payload.
type publicInvestigationStep struct {
	ArgsSummary json.RawMessage `json:"argsSummary,omitempty"`
	StepID      uuid.UUID       `json:"stepId"`
	Name        string          `json:"toolName"`
	ArgsDigest  string          `json:"argsDigest"`
	State       string          `json:"state"`
	Result      json.RawMessage `json:"result,omitempty"`
	ErrorCode   string          `json:"errorCode,omitempty"`
}

// publicInvestigationStepViews projects committed steps onto the public contract.
func publicInvestigationStepViews(steps []investigation.Step) []publicInvestigationStep {
	out := make([]publicInvestigationStep, 0, len(steps))
	for _, step := range steps {
		out = append(out, publicInvestigationStepView(step))
	}
	return out
}

// publicInvestigationStepView projects one committed step. The stored bytes are
// never rewritten: the projection only builds the public answer of the read.
func publicInvestigationStepView(step investigation.Step) publicInvestigationStep {
	view := publicInvestigationStep{
		ArgsSummary: step.ArgsSummary,
		StepID:      step.StepID,
		Name:        step.Name,
		ArgsDigest:  step.ArgsDigest,
		State:       step.State,
		ErrorCode:   step.ErrorCode,
	}
	view.Result = publicStepResult(step.Name, step.Result)
	return view
}

// publicStepResult projects the stored tool result of one step.
//
// The envelope of a tool response is public: the tool name, the state, the
// degraded sources, the evidence references and the budget are the facts the
// workbench shows. The payload is projected per tool, because the tools do not
// share a payload contract.
func publicStepResult(tool string, raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var envelope map[string]any
	if json.Unmarshal(raw, &envelope) != nil {
		return nil
	}
	// The model step does not use the tool response envelope: it keeps the raw
	// provider response. The response may repeat material of the model input,
	// so only the provider and the usage state are public. The validated answer
	// itself is published with the job.
	if tool == "model" {
		out := map[string]any{}
		for _, key := range []string{"provider", "usageKnown"} {
			if value, ok := envelope[key]; ok {
				out[key] = value
			}
		}
		return marshalPublicStepResult(out)
	}
	out := map[string]any{}
	for _, key := range []string{"schemaVersion", "tool", "state", "partial", "errorCode", "evidenceRefs", "degradedSources", "budget", "summary"} {
		if value, ok := envelope[key]; ok {
			out[key] = value
		}
	}
	if data, ok := envelope["data"]; ok {
		out["data"] = publicStepData(tool, data)
	}
	return marshalPublicStepResult(out)
}

func marshalPublicStepResult(out map[string]any) json.RawMessage {
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil
	}
	return encoded
}

// publicStepData projects the payload of a tool result.
//
// The assistant context tool returns the frozen model input: the conversation
// binding with the effective grant snapshot, the message history and the
// analysis catalog. Its public subset is the focus of the investigation and
// the version of the context that was read. The resource investigation context
// carries the same admission digests and keeps only the resolved resources and
// the related incidents. Every other tool keeps its payload, minus the
// internal execution context that no tool result may publish.
func publicStepData(tool string, data any) any {
	switch tool {
	case "get_assistant_context":
		return publicAssistantContextData(data)
	case "get_resource_investigation_context":
		return publicResourceInvestigationContextData(data)
	case "execute_analysis_query":
		// The persisted scope authorizes recovery; public results expose its
		// digest and the separately authorized queryScope descriptor instead.
		if fields, ok := data.(map[string]any); ok {
			out := make(map[string]any, len(fields))
			for key, value := range fields {
				if key != "scope" {
					out[key] = stripInternalStepContext(value)
				}
			}
			return out
		}
		return nil
	default:
		return stripInternalStepContext(data)
	}
}

// publicAssistantContextData keeps the documented public fields of the
// assistant context. An unknown field of the context is not passed through: the
// context is internal model input.
func publicAssistantContextData(data any) any {
	fields, ok := data.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]any{}
	for _, key := range []string{"focus", "schemaVersion", "requestedAt"} {
		if value, ok := fields[key]; ok {
			out[key] = value
		}
	}
	return out
}

// publicResourceInvestigationContextData keeps the resolved resources and the
// related incidents of the resource investigation context.
func publicResourceInvestigationContextData(data any) any {
	fields, ok := data.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]any{}
	for _, key := range []string{"relatedIncidents", "relatedIncidentsTruncated"} {
		if value, ok := fields[key]; ok {
			out[key] = value
		}
	}
	if context, ok := fields["context"]; ok {
		out["context"] = stripInternalStepContext(context)
	}
	return out
}

// internalStepContextKeys are the keys of internal admission material. They
// carry the effective grant snapshot, the admission digests, the effective
// budget and the qualification metadata of the sources. No public step result
// may publish them.
//
// The keys are matched by contract, never by a value pattern, and they never
// name the input digest of a fixed RCA revision: that digest is the provenance
// of a public business fact and stays in the payload. A business field that
// happens to be called "message" or "history" is not touched either; only the
// context tools publish their model input, and they do so through their own
// explicit public subset.
var internalStepContextKeys = map[string]bool{
	"binding":                     true,
	"grantSnapshot":               true,
	"grantSnapshotDigest":         true,
	"executionScopeDigest":        true,
	"contextDigest":               true,
	"effectiveBudget":             true,
	"analysisCatalog":             true,
	"analysisDatasets":            true,
	"sourceCapabilities":          true,
	"networkCapabilities":         true,
	"historyManifestDigest":       true,
	"faultChainEvaluationRequest": true,
}

// stripInternalStepContext removes the internal execution context of a tool
// payload. The keys are matched by contract, never by a value pattern, so a
// legitimate business field that shares a name survives.
func stripInternalStepContext(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if internalStepContextKeys[key] {
				continue
			}
			out[key] = stripInternalStepContext(item)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, stripInternalStepContext(item))
		}
		return out
	default:
		return value
	}
}
