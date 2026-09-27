package profile

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"ops-platform/internal/integrations"
	"ops-platform/internal/integrations/victorialogs"
	"ops-platform/internal/integrations/victoriametrics"
	"ops-platform/internal/integrations/vmalert"
)

type SourceCandidate struct {
	Component string
	Endpoint  string
	AuthRef   string
	LogicalID string
	Namespace string
	Name      string
}

// DiscoverSources probes a bounded read-only API for each unique source. An
// unreachable or unauthorized candidate is an error, never evidence of absence.
func DiscoverSources(ctx context.Context, candidates []SourceCandidate, client integrations.HTTPDoer) (map[string]integrations.ProbeResult, error) {
	seenComponent := make(map[string]bool, len(candidates))
	seenEndpoint := make(map[string]string, len(candidates))
	for _, candidate := range candidates {
		if candidate.Component == "" || candidate.Endpoint == "" {
			return nil, fmt.Errorf("SOURCE_ENDPOINT_INVALID: component and endpoint are required")
		}
		if seenComponent[candidate.Component] {
			return nil, fmt.Errorf("SOURCE_CONFLICT: multiple %s instances were discovered", candidate.Component)
		}
		seenComponent[candidate.Component] = true
		if other, exists := seenEndpoint[candidate.Endpoint]; exists && other != candidate.Component {
			return nil, fmt.Errorf("SOURCE_CONFLICT: endpoint is shared by %s and %s", other, candidate.Component)
		}
		seenEndpoint[candidate.Endpoint] = candidate.Component
	}

	results := make(map[string]integrations.ProbeResult, len(candidates))
	for _, candidate := range candidates {
		logicalID := candidate.LogicalID
		if logicalID == "" && candidate.Namespace != "" && candidate.Name != "" {
			logicalID = candidate.Namespace + "/" + candidate.Name
		}
		var result integrations.ProbeResult
		var err error
		switch candidate.Component {
		case "victoriaMetrics":
			result, err = victoriametrics.Probe(ctx, client, candidate.Endpoint, candidate.AuthRef, logicalID)
		case "victoriaLogs":
			result, err = victorialogs.Probe(ctx, client, candidate.Endpoint, candidate.AuthRef, logicalID)
		case "vmalert":
			result, err = vmalert.Probe(ctx, client, candidate.Endpoint, candidate.AuthRef, logicalID)
		default:
			return nil, fmt.Errorf("SOURCE_COMPONENT_UNSUPPORTED: %q", candidate.Component)
		}
		if err != nil {
			return nil, fmt.Errorf("%s probe failed: %w", candidate.Component, err)
		}
		results[candidate.Component] = result
	}
	return results, nil
}

type kubectlProxyTransport struct {
	reader     kubectlReader
	byEndpoint map[string]SourceCandidate
}

func (transport kubectlProxyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method != http.MethodGet {
		return nil, fmt.Errorf("source discovery only permits read-only GET requests")
	}
	candidate, ok := transport.byEndpoint[request.URL.Host]
	if !ok {
		return nil, fmt.Errorf("no discovered Kubernetes Service matches probe host %q", request.URL.Host)
	}
	parsed, err := url.Parse(candidate.Endpoint)
	if err != nil {
		return nil, err
	}
	port := parsed.Port()
	if port == "" {
		return nil, fmt.Errorf("candidate endpoint has no port")
	}
	proxyPath := fmt.Sprintf("/api/v1/namespaces/%s/services/http:%s:%s/proxy%s", url.PathEscape(candidate.Namespace), url.PathEscape(candidate.Name), port, request.URL.RequestURI())
	body, err := transport.reader.raw(request.Context(), proxyPath)
	status := http.StatusOK
	if err != nil {
		message := strings.ToLower(err.Error())
		switch {
		case strings.Contains(message, "unauthorized") || strings.Contains(message, "401"):
			status, body, err = http.StatusUnauthorized, []byte("unauthorized"), nil
		case strings.Contains(message, "forbidden") || strings.Contains(message, "403"):
			status, body, err = http.StatusForbidden, []byte("forbidden"), nil
		default:
			return nil, err
		}
	}
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(string(body))),
		Request:    request,
	}, nil
}

func enrichVictoriaCandidates(ctx context.Context, reader kubectlReader, components map[string][]ComponentCandidate) {
	for _, component := range []string{"victoriaMetrics", "victoriaLogs", "vmalert"} {
		candidates := components[component]
		if len(candidates) == 0 {
			continue
		}
		sources := make([]SourceCandidate, 0, len(candidates))
		transportMap := make(map[string]SourceCandidate, len(candidates))
		for _, candidate := range candidates {
			source := SourceCandidate{Component: component, Endpoint: candidate.Endpoint, Namespace: candidate.Namespace, Name: candidate.Name, LogicalID: candidate.Namespace + "/" + candidate.Name}
			sources = append(sources, source)
			if parsed, err := url.Parse(candidate.Endpoint); err == nil {
				transportMap[parsed.Host] = source
			}
		}
		client := &http.Client{Transport: kubectlProxyTransport{reader: reader, byEndpoint: transportMap}}
		probes, err := DiscoverSources(ctx, sources, client)
		for index := range candidates {
			if err != nil {
				candidates[index].Compatible = false
				candidates[index].Evidence = append(candidates[index].Evidence, err.Error())
				continue
			}
			result := probes[component]
			candidates[index].Version = result.Version
			for capability, state := range result.Capabilities {
				candidates[index].Evidence = append(candidates[index].Evidence, fmt.Sprintf("read-only capability %s=%s", capability, state))
			}
			candidates[index].Evidence = append(candidates[index].Evidence, "logical source ID "+result.LogicalID)
			candidates[index].Evidence = append(candidates[index].Evidence, "version read from upstream service root or application metrics")
		}
		components[component] = candidates
	}
}
