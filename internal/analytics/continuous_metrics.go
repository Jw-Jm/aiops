package analytics

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

var podMetricNames = map[string]string{"metrics.pod-cpu": "container_cpu_usage_seconds_total", "metrics.pod-memory": "container_memory_working_set_bytes", "metrics.pod-restarts": "kube_pod_container_status_restarts_total", "metrics.pod-phase": "kube_pod_status_phase"}

func (v *Victoria) ContinuousMetrics() bool {
	if !v.binding.KubernetesMetrics() {
		return false
	}
	for _, q := range v.qualifications {
		if q.Continuous {
			return true
		}
	}
	return false
}

// Qualify stored sample times, rather than Prometheus evaluation times or target health.
func (v *Victoria) RefreshContinuousMetrics(ctx context.Context) (err error) {
	if !v.ContinuousMetrics() {
		return ErrInvalid
	}
	defer func() {
		if err != nil {
			v.liveMu.Lock()
			v.liveUntil = time.Time{}
			v.liveMu.Unlock()
		}
	}()
	if v.authorize(ctx, v.binding) != nil {
		return ErrDenied
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	names := []string{}
	for _, name := range podMetricNames {
		names = append(names, name)
	}
	sort.Strings(names)
	selectors := []string{`__name__=~` + strconv.Quote(strings.Join(names, "|")), `uid!=""`}
	keys := []string{}
	for k := range v.binding.ScopeMapping.RequiredLabels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !safeLabel.MatchString(k) {
			return ErrDenied
		}
		selectors = append(selectors, k+"="+strconv.Quote(v.binding.ScopeMapping.RequiredLabels[k]))
	}
	namespaces := v.binding.ScopeMapping.Scopes["namespace"]
	if len(namespaces) == 0 || len(v.binding.ScopeMapping.Scopes["cluster"]) != 1 {
		return ErrDenied
	}
	parts := []string{}
	for _, ns := range namespaces {
		parts = append(parts, regexp.QuoteMeta(ns))
	}
	selectors = append(selectors, `namespace=~`+strconv.Quote(strings.Join(parts, "|")))
	expression := "topk by (__name__) (1, timestamp({" + strings.Join(selectors, ",") + "}) keep_metric_names)"
	request, e := http.NewRequestWithContext(ctx, "GET", strings.TrimSuffix(v.binding.Endpoint, "/")+"/api/v1/query?"+url.Values{"query": {expression}, "nocache": {"1"}}.Encode(), nil)
	if e != nil {
		return ErrInvalid
	}
	response, e := v.client.Do(request)
	if e != nil {
		return ErrSource
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return ErrSource
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, 65537))
	if e != nil || len(raw) > 65536 {
		return ErrSource
	}
	var out struct {
		Status   string   `json:"status"`
		Partial  bool     `json:"isPartial"`
		Warnings []string `json:"warnings"`
		Data     struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  []json.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &out) != nil || out.Status != "success" || out.Partial || len(out.Warnings) > 0 || out.Data.ResultType != "vector" || len(out.Data.Result) != len(names) {
		return ErrSource
	}
	now := time.Now().UTC()
	watermark := now
	seen := map[string]bool{}
	for _, row := range out.Data.Result {
		name := row.Metric["__name__"]
		if !slices.Contains(names, name) || seen[name] || len(row.Value) != 2 {
			return ErrSource
		}
		seen[name] = true
		for _, k := range keys {
			if row.Metric[k] != v.binding.ScopeMapping.RequiredLabels[k] {
				return ErrDenied
			}
		}
		if row.Metric["tenant"] != v.binding.Tenant || row.Metric["cluster"] != v.binding.ScopeMapping.Scopes["cluster"][0] || !slices.Contains(namespaces, row.Metric["namespace"]) || uuid.Validate(row.Metric["uid"]) != nil {
			return ErrDenied
		}
		var text string
		if json.Unmarshal(row.Value[1], &text) != nil {
			return ErrSource
		}
		seconds, e := strconv.ParseFloat(text, 64)
		if e != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
			return ErrSource
		}
		at := time.UnixMilli(int64(seconds * 1000)).UTC()
		if at.After(now) || at.Before(now.Add(-45*time.Second)) {
			return ErrSource
		}
		if at.Before(watermark) {
			watermark = at
		}
	}
	if v.authorize(ctx, v.binding) != nil {
		return ErrDenied
	}
	v.liveMu.Lock()
	v.liveTo = watermark
	v.liveUntil = now.Add(30 * time.Second)
	v.liveMu.Unlock()
	return nil
}
