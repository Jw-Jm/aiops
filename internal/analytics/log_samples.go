package analytics

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"ops-platform/internal/contract"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

func (v *Victoria) executeLogSamples(ctx context.Context, p VictoriaPlan) (QueryResult, error) {
	if p.digest == "" || v.binding.SourceType != "victorialogs" || !p.qualification.VerifiedUntil.After(time.Now()) || v.authorize(ctx, v.binding) != nil {
		return QueryResult{}, ErrDenied
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	select {
	case v.slots <- struct{}{}:
		defer func() { <-v.slots }()
	case <-ctx.Done():
		return QueryResult{}, ErrSource
	}
	params := url.Values{"query": {p.query}, "allow_partial_response": {"0"}, "start": {p.intent.TimeRange.From.UTC().Format(time.RFC3339Nano)}, "end": {p.intent.TimeRange.To.UTC().Format(time.RFC3339Nano)}}
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimSuffix(v.binding.Endpoint, "/")+p.path, strings.NewReader(params.Encode()))
	if err != nil {
		return QueryResult{}, ErrInvalid
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := v.client.Do(req)
	if err != nil {
		return QueryResult{}, ErrSource
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return QueryResult{}, ErrSource
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(body) > 64<<10 {
		return QueryResult{}, ErrSource
	}
	out := QueryResult{SchemaVersion: "query-result/v1", DatasetID: p.intent.DatasetID, PlanDigest: p.digest, CatalogDigest: p.catalogDigest, ScopeDigest: graph.ScopeDigest(p.scope), Status: "partial", Exact: false, Unit: p.unit, Rows: []Row{}, Samples: []LogSample{}, SnapshotReadAt: time.Now().UTC(), Freshness: "fresh", TimeRange: copyQueryTimeRange(p.intent.TimeRange), Warnings: []string{"bounded_sanitized_samples_not_total_count"}, DegradedSources: []string{}, SourceManifest: &SourceManifest{v.binding.SourceID, v.binding.Revision, evidence.BindingScopeDigest(v.binding), graph.ScopeDigest(p.qualification), graph.ScopeDigest(p.query), p.intent.DatasetID + "-samples/v1", p.intent.TimeRange.From, p.intent.TimeRange.To}}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 4096), 64<<10)
	count := 0
	for scanner.Scan() {
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		count++
		if count > p.intent.Limit+1 {
			return QueryResult{}, ErrSource
		}
		var entry map[string]string
		if json.Unmarshal(scanner.Bytes(), &entry) != nil || entry["tenant"] != p.scope.Tenant || entry["cluster"] != p.scope.Cluster || !slices.Contains(p.scope.Namespaces, entry["namespace"]) || entry["uid"] == "" {
			return QueryResult{}, ErrSource
		}
		if level := p.intent.Filters; len(level) > 0 {
			for _, filter := range level {
				if (filter.Field == "level" && entry["level"] != filter.Value) || (filter.Field == "namespace" && entry["namespace"] != filter.Value) {
					return QueryResult{}, ErrSource
				}
			}
		}
		for k, value := range v.binding.ScopeMapping.RequiredLabels {
			if entry[k] != value {
				return QueryResult{}, ErrDenied
			}
		}
		if len(p.scope.Resources) > 0 {
			allowed := false
			for _, id := range p.scope.Resources {
				parsed, err := resource.ParseCanonicalID(id)
				if err == nil && parsed.StableID == entry["uid"] {
					allowed = true
				}
			}
			if !allowed {
				return QueryResult{}, ErrDenied
			}
		}
		at, err := time.Parse(time.RFC3339Nano, entry["_time"])
		if err != nil || at.Before(p.intent.TimeRange.From) || !at.Before(p.intent.TimeRange.To) {
			return QueryResult{}, ErrSource
		}
		if count > p.intent.Limit {
			out.Truncated = true
			continue
		}
		text := evidence.Redact(entry["_msg"])
		if !utf8.ValidString(text) {
			return QueryResult{}, ErrSource
		}
		runes := []rune(text)
		if len(runes) > 2048 {
			text = string(runes[:2048])
			out.Truncated = true
		}
		level := entry["level"]
		if !slices.Contains([]string{"error", "warn", "info", "debug"}, level) {
			level = "unknown"
		}
		out.Samples = append(out.Samples, LogSample{at.UTC(), text, level})
	}
	if scanner.Err() != nil {
		return QueryResult{}, ErrSource
	}
	out.Rows = []Row{{Value: float64(len(out.Samples))}}
	if len(out.Samples) == 0 {
		out.Rows = []Row{}
		out.Status = "no_data"
		out.Warnings = append(out.Warnings, "no_observations")
	}
	if v.authorize(ctx, v.binding) != nil || !p.qualification.VerifiedUntil.After(time.Now()) {
		return QueryResult{}, ErrDenied
	}
	if contract.Validate("https://ops.local/schemas/query-result/v1", rawResult(out)) != nil {
		return QueryResult{}, ErrSource
	}
	return out, nil
}
