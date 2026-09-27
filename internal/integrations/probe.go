package integrations

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// ProbeResult contains only non-secret source identity and capability state.
// AuthRef is an opaque Secret reference; credentials never belong in this value.
type ProbeResult struct {
	Endpoint     string            `json:"endpoint" yaml:"endpoint"`
	Version      string            `json:"version" yaml:"version"`
	Capabilities map[string]string `json:"capabilities" yaml:"capabilities"`
	AuthRef      string            `json:"authRef,omitempty" yaml:"authRef,omitempty"`
	LogicalID    string            `json:"logicalId" yaml:"logicalId"`
}

// HTTPDoer allows a Kubernetes API proxy transport or a test server to provide
// read-only HTTP responses without exposing credentials to probe code.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

var releaseVersionPattern = regexp.MustCompile(`\bVersion\s+(?:[^\s<]*?-tags-)?(v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?)\b`)
var gitDescribeSuffix = regexp.MustCompile(`-[0-9]+-g[0-9a-fA-F]{7,}$`)

func NewHTTPClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Second}
}

func Get(ctx context.Context, client HTTPDoer, endpoint, path, authRef string) ([]byte, error) {
	if client == nil {
		client = NewHTTPClient()
	}
	base, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, fmt.Errorf("SOURCE_ENDPOINT_INVALID: endpoint must be an absolute HTTP(S) URL without userinfo or query")
	}
	probePath, err := url.Parse(path)
	if err != nil || probePath.IsAbs() || probePath.Host != "" || probePath.Fragment != "" {
		return nil, fmt.Errorf("SOURCE_ENDPOINT_INVALID: probe path must be relative")
	}
	requestURL := *base
	requestURL.Path = strings.TrimSuffix(base.Path, "/") + "/" + strings.TrimPrefix(probePath.Path, "/")
	requestURL.RawPath = ""
	requestURL.RawQuery = probePath.RawQuery
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("SOURCE_ENDPOINT_INVALID: build request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("SOURCE_UNAVAILABLE: read-only GET failed (authRef=%q): %w", authRef, err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("SOURCE_UNAUTHORIZED: read-only GET returned HTTP %d (authRef=%q)", response.StatusCode, authRef)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("SOURCE_PROBE_REJECTED: read-only GET returned HTTP %d", response.StatusCode)
	}
	const maxProbeBody = 1 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, maxProbeBody+1))
	if err != nil {
		return nil, fmt.Errorf("SOURCE_UNAVAILABLE: read response body: %w", err)
	}
	if len(body) > maxProbeBody {
		return nil, fmt.Errorf("SOURCE_PROBE_REJECTED: response exceeds %d bytes", maxProbeBody)
	}
	return body, nil
}

func VersionFromRoot(body []byte) (string, error) {
	match := releaseVersionPattern.FindSubmatch(body)
	if len(match) != 2 {
		return "", fmt.Errorf("SOURCE_VERSION_UNKNOWN: service root did not expose an exact release version")
	}
	return gitDescribeSuffix.ReplaceAllString(string(match[1]), ""), nil
}

func Healthy(ctx context.Context, client HTTPDoer, endpoint, authRef string) error {
	body, err := Get(ctx, client, endpoint, "/health", authRef)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(body)) != "OK" {
		return fmt.Errorf("SOURCE_UNHEALTHY: /health returned an unexpected response")
	}
	return nil
}

func Version(ctx context.Context, client HTTPDoer, endpoint, authRef string) (string, error) {
	body, err := Get(ctx, client, endpoint, "/", authRef)
	if err != nil {
		return "", err
	}
	return VersionFromRoot(body)
}

func VersionFromVictoriaMetricsMetrics(body []byte) (string, error) {
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(line, "vm_app_version{") {
			continue
		}
		for _, label := range strings.Split(strings.TrimSuffix(strings.SplitN(line, "}", 2)[0], "{"), ",") {
			key, value, ok := strings.Cut(strings.TrimSpace(label), "=")
			if !ok || key != "short_version" {
				continue
			}
			value = strings.Trim(value, `"`)
			match := releaseVersionPattern.FindStringSubmatch("Version " + value)
			if len(match) == 2 {
				return gitDescribeSuffix.ReplaceAllString(match[1], ""), nil
			}
		}
	}
	const versionPrefix = `app_version="victoria-metrics-`
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.Contains(line, "vm_log_messages_total{") || !strings.Contains(line, versionPrefix) {
			continue
		}
		start := strings.Index(line, versionPrefix) + len(versionPrefix)
		value := line[start:]
		end := strings.IndexByte(value, '"')
		if end < 0 {
			continue
		}
		match := releaseVersionPattern.FindStringSubmatch("Version " + value[:end])
		if len(match) == 2 {
			return gitDescribeSuffix.ReplaceAllString(match[1], ""), nil
		}
	}
	return "", fmt.Errorf("SOURCE_VERSION_UNKNOWN: /metrics has no exact vm_app_version short_version or vm_log_messages_total app_version")
}
